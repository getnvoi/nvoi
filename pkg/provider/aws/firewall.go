package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ensureSecurityGroup finds or creates a security group with nvoi default rules.
func (c *Client) ensureSecurityGroup(ctx context.Context, name, vpcID string, labels map[string]string) (string, error) {
	if name == "" {
		return "", nil
	}

	existing, err := c.findSecurityGroupByName(ctx, name, vpcID)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return deref(existing.GroupId), nil
	}

	resp, err := c.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:         aws.String(name),
		Description:       aws.String("Managed by nvoi"),
		VpcId:             aws.String(vpcID),
		TagSpecifications: tagSpec(ec2types.ResourceTypeSecurityGroup, name, labels),
	})
	if err != nil {
		return "", fmt.Errorf("create security group: %w", err)
	}
	sgID := deref(resp.GroupId)

	// Add nvoi default ingress rules
	_, err = c.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String(sgID),
		IpPermissions: defaultIngressRules(),
	})
	if err != nil {
		return "", fmt.Errorf("add ingress rules: %w", err)
	}

	return sgID, nil
}

func (c *Client) deleteSecurityGroup(ctx context.Context, name string) error {
	sg, err := c.findSecurityGroupByName(ctx, name, "")
	if err != nil || sg == nil {
		return nil
	}
	_, err = c.ec2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: sg.GroupId,
	})
	return err
}

func (c *Client) ListAllFirewalls(ctx context.Context) ([]*provider.Firewall, error) {
	resp, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{})
	if err != nil {
		return nil, fmt.Errorf("list security groups: %w", err)
	}
	var out []*provider.Firewall
	for _, sg := range resp.SecurityGroups {
		name := deref(sg.GroupName)
		if strings.HasPrefix(name, "nvoi-") {
			out = append(out, &provider.Firewall{ID: deref(sg.GroupId), Name: name})
		}
	}
	return out, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func (c *Client) findSecurityGroupByName(ctx context.Context, name, vpcID string) (*ec2types.SecurityGroup, error) {
	filters := []ec2types.Filter{
		{Name: aws.String("group-name"), Values: []string{name}},
	}
	if vpcID != "" {
		filters = append(filters, ec2types.Filter{
			Name: aws.String("vpc-id"), Values: []string{vpcID},
		})
	}
	resp, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: filters,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.SecurityGroups) > 0 {
		return &resp.SecurityGroups[0], nil
	}
	return nil, nil
}

func defaultIngressRules() []ec2types.IpPermission {
	pub := []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}
	priv := []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16")}}

	return []ec2types.IpPermission{
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(22), ToPort: aws.Int32(22), IpRanges: pub},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(80), ToPort: aws.Int32(80), IpRanges: pub},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(443), ToPort: aws.Int32(443), IpRanges: pub},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(6443), ToPort: aws.Int32(6443), IpRanges: priv},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(10250), ToPort: aws.Int32(10250), IpRanges: priv},
		{IpProtocol: aws.String("udp"), FromPort: aws.Int32(8472), ToPort: aws.Int32(8472), IpRanges: priv},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(5000), ToPort: aws.Int32(5000), IpRanges: priv},
	}
}
