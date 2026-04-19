package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/getnvoi/nvoi/pkg/provider"
)

func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	var groups []provider.ResourceGroup

	// Servers
	servers, err := c.ListServers(ctx, nil)
	if err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "Instances", Columns: []string{"ID", "Name", "Status", "IPv4", "Private IP"}}
	for _, s := range servers {
		g.Rows = append(g.Rows, []string{s.ID, s.Name, string(s.Status), s.IPv4, s.PrivateIP})
	}
	groups = append(groups, g)

	// Security Groups
	firewalls, err := c.ListAllFirewalls(ctx)
	if err != nil {
		return nil, err
	}
	g = provider.ResourceGroup{Name: "Security Groups", Columns: []string{"ID", "Name"}}
	for _, fw := range firewalls {
		g.Rows = append(g.Rows, []string{fw.ID, fw.Name})
	}
	groups = append(groups, g)

	// VPCs
	networks, err := c.ListAllNetworks(ctx)
	if err != nil {
		return nil, err
	}
	g = provider.ResourceGroup{Name: "VPCs", Columns: []string{"ID", "Name"}}
	for _, n := range networks {
		g.Rows = append(g.Rows, []string{n.ID, n.Name})
	}
	groups = append(groups, g)

	// Subnets (in nvoi VPCs)
	for _, n := range networks {
		subnets, err := c.listSubnets(ctx, n.ID)
		if err != nil {
			continue
		}
		g = provider.ResourceGroup{Name: "Subnets", Columns: []string{"ID", "Name", "VPC", "CIDR", "AZ"}}
		for _, sub := range subnets {
			g.Rows = append(g.Rows, sub)
		}
		groups = append(groups, g)
	}

	// Internet Gateways (in nvoi VPCs)
	for _, n := range networks {
		igws, err := c.listInternetGateways(ctx, n.ID)
		if err != nil {
			continue
		}
		g = provider.ResourceGroup{Name: "Internet Gateways", Columns: []string{"ID", "Name", "VPC"}}
		for _, igw := range igws {
			g.Rows = append(g.Rows, igw)
		}
		groups = append(groups, g)
	}

	// Route Tables (in nvoi VPCs, non-main only)
	for _, n := range networks {
		rtbs, err := c.listRouteTables(ctx, n.ID)
		if err != nil {
			continue
		}
		g = provider.ResourceGroup{Name: "Route Tables", Columns: []string{"ID", "Name", "VPC"}}
		for _, rtb := range rtbs {
			g.Rows = append(g.Rows, rtb)
		}
		groups = append(groups, g)
	}

	// Volumes
	volumes, err := c.ListVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}
	g = provider.ResourceGroup{Name: "EBS Volumes", Columns: []string{"ID", "Name", "Size", "AZ", "Server"}}
	for _, v := range volumes {
		g.Rows = append(g.Rows, []string{v.ID, v.Name, fmt.Sprintf("%dGB", v.Size), v.Location, v.ServerID})
	}
	groups = append(groups, g)

	return groups, nil
}

// ── Resource listing helpers ──────────────────────────────────────────────────

func (c *Client) listSubnets(ctx context.Context, vpcID string) ([][]string, error) {
	resp, err := c.ec2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, sub := range resp.Subnets {
		name := tagName(sub.Tags)
		rows = append(rows, []string{deref(sub.SubnetId), name, vpcID, deref(sub.CidrBlock), deref(sub.AvailabilityZone)})
	}
	return rows, nil
}

func (c *Client) listInternetGateways(ctx context.Context, vpcID string) ([][]string, error) {
	resp, err := c.ec2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, igw := range resp.InternetGateways {
		name := tagName(igw.Tags)
		rows = append(rows, []string{deref(igw.InternetGatewayId), name, vpcID})
	}
	return rows, nil
}

func (c *Client) listRouteTables(ctx context.Context, vpcID string) ([][]string, error) {
	resp, err := c.ec2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, rtb := range resp.RouteTables {
		isMain := false
		for _, assoc := range rtb.Associations {
			if assoc.Main != nil && *assoc.Main {
				isMain = true
			}
		}
		if isMain {
			continue // skip VPC default route table
		}
		name := tagName(rtb.Tags)
		rows = append(rows, []string{deref(rtb.RouteTableId), name, vpcID})
	}
	return rows, nil
}

func tagName(tags []ec2types.Tag) string {
	for _, tag := range tags {
		if deref(tag.Key) == "Name" {
			return deref(tag.Value)
		}
	}
	return ""
}
