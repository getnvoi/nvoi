package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ensureVPC finds or creates a VPC + subnet + IGW + route table + route + association.
// Returns (vpcID, subnetID, error).
func (c *Client) ensureVPC(ctx context.Context, name string, labels map[string]string) (string, string, error) {
	if name == "" {
		return "", "", nil
	}

	// Find existing
	vpc, err := c.findVPCByName(ctx, name)
	if err != nil {
		return "", "", err
	}
	if vpc != nil {
		subnetID, err := c.findSubnetInVPC(ctx, deref(vpc.VpcId))
		if err != nil {
			return "", "", err
		}
		return deref(vpc.VpcId), subnetID, nil
	}

	// Create VPC
	vpcResp, err := c.ec2.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock:         aws.String(utils.PrivateNetworkCIDR),
		TagSpecifications: tagSpec(ec2types.ResourceTypeVpc, name, labels),
	})
	if err != nil {
		return "", "", fmt.Errorf("create vpc: %w", err)
	}
	vpcID := deref(vpcResp.Vpc.VpcId)

	// Enable DNS hostnames
	_, _ = c.ec2.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{
		VpcId:              aws.String(vpcID),
		EnableDnsHostnames: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
	})

	// Create subnet — pinned to {region}a so volumes and instances always share the same AZ.
	subnetResp, err := c.ec2.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String(utils.PrivateNetworkSubnet),
		AvailabilityZone: aws.String(c.region + "a"),
		TagSpecifications: tagSpec(ec2types.ResourceTypeSubnet, name+"-subnet", labels),
	})
	if err != nil {
		return "", "", fmt.Errorf("create subnet: %w", err)
	}
	subnetID := deref(subnetResp.Subnet.SubnetId)

	// Enable auto-assign public IP on subnet
	_, _ = c.ec2.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{
		SubnetId:            aws.String(subnetID),
		MapPublicIpOnLaunch: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
	})

	// Create internet gateway
	igwResp, err := c.ec2.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
		TagSpecifications: tagSpec(ec2types.ResourceTypeInternetGateway, name+"-igw", labels),
	})
	if err != nil {
		return "", "", fmt.Errorf("create igw: %w", err)
	}
	igwID := deref(igwResp.InternetGateway.InternetGatewayId)

	// Attach IGW to VPC
	_, err = c.ec2.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		VpcId:             aws.String(vpcID),
		InternetGatewayId: aws.String(igwID),
	})
	if err != nil {
		return "", "", fmt.Errorf("attach igw: %w", err)
	}

	// Create route table
	rtbResp, err := c.ec2.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
		VpcId:             aws.String(vpcID),
		TagSpecifications: tagSpec(ec2types.ResourceTypeRouteTable, name+"-rtb", labels),
	})
	if err != nil {
		return "", "", fmt.Errorf("create route table: %w", err)
	}
	rtbID := deref(rtbResp.RouteTable.RouteTableId)

	// Add default route to IGW
	_, err = c.ec2.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igwID),
	})
	if err != nil {
		return "", "", fmt.Errorf("create route: %w", err)
	}

	// Associate route table with subnet
	_, err = c.ec2.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtbID),
		SubnetId:     aws.String(subnetID),
	})
	if err != nil {
		return "", "", fmt.Errorf("associate route table: %w", err)
	}

	return vpcID, subnetID, nil
}

// deleteVPC cascades: detach+delete IGW, delete subnet, delete route table, delete VPC.
func (c *Client) deleteVPC(ctx context.Context, name string) error {
	vpc, err := c.findVPCByName(ctx, name)
	if err != nil || vpc == nil {
		return nil
	}
	vpcID := deref(vpc.VpcId)

	// Detach and delete internet gateways
	igwResp, _ := c.ec2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}},
		},
	})
	if igwResp != nil {
		for _, igw := range igwResp.InternetGateways {
			c.ec2.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
				InternetGatewayId: igw.InternetGatewayId,
				VpcId:             aws.String(vpcID),
			})
			c.ec2.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
				InternetGatewayId: igw.InternetGatewayId,
			})
		}
	}

	// Delete subnets
	subResp, _ := c.ec2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if subResp != nil {
		for _, sub := range subResp.Subnets {
			c.ec2.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: sub.SubnetId})
		}
	}

	// Delete non-main route tables
	rtbResp, _ := c.ec2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if rtbResp != nil {
		for _, rtb := range rtbResp.RouteTables {
			isMain := false
			for _, assoc := range rtb.Associations {
				if assoc.Main != nil && *assoc.Main {
					isMain = true
				} else if assoc.RouteTableAssociationId != nil {
					c.ec2.DisassociateRouteTable(ctx, &ec2.DisassociateRouteTableInput{
						AssociationId: assoc.RouteTableAssociationId,
					})
				}
			}
			if !isMain {
				c.ec2.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{RouteTableId: rtb.RouteTableId})
			}
		}
	}

	// Delete VPC
	_, err = c.ec2.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)})
	return err
}

func (c *Client) ListAllNetworks(ctx context.Context) ([]*provider.Network, error) {
	resp, err := c.ec2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{})
	if err != nil {
		return nil, fmt.Errorf("list vpcs: %w", err)
	}
	var out []*provider.Network
	for _, vpc := range resp.Vpcs {
		name := ""
		for _, tag := range vpc.Tags {
			if deref(tag.Key) == "Name" {
				name = deref(tag.Value)
			}
		}
		if strings.HasPrefix(name, "nvoi-") {
			out = append(out, &provider.Network{ID: deref(vpc.VpcId), Name: name})
		}
	}
	return out, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func (c *Client) findVPCByName(ctx context.Context, name string) (*ec2types.Vpc, error) {
	resp, err := c.ec2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: nameTag(name),
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Vpcs) > 0 {
		return &resp.Vpcs[0], nil
	}
	return nil, nil
}

func (c *Client) findSubnetInVPC(ctx context.Context, vpcID string) (string, error) {
	resp, err := c.ec2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Subnets) > 0 {
		return deref(resp.Subnets[0].SubnetId), nil
	}
	return "", fmt.Errorf("no subnet found in vpc %s", vpcID)
}
