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
// When the SG already exists, rules are reconciled to match the desired set.
func (c *Client) ensureSecurityGroup(ctx context.Context, name, vpcID string, labels map[string]string) (string, error) {
	if name == "" {
		return "", nil
	}

	existing, err := c.findSecurityGroupByName(ctx, name, vpcID)
	if err != nil {
		return "", err
	}
	if existing != nil {
		sgID := deref(existing.GroupId)
		if err := c.reconcileIngressRules(ctx, sgID); err != nil {
			return "", fmt.Errorf("reconcile firewall rules: %w", err)
		}
		return sgID, nil
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
		IpPermissions: baseIngressRules(),
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

// reconcileIngressRules replaces all ingress rules on an existing security group
// to match the desired nvoi defaults. Revokes current rules, then authorizes the
// desired set. Idempotent — same rules in = no-op (AWS ignores duplicate revoke/auth).
func (c *Client) reconcileIngressRules(ctx context.Context, sgID string) error {
	resp, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	if err != nil {
		return fmt.Errorf("describe security group: %w", err)
	}
	if len(resp.SecurityGroups) == 0 {
		return nil
	}

	current := resp.SecurityGroups[0].IpPermissions
	if len(current) > 0 {
		_, err = c.ec2.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId:       aws.String(sgID),
			IpPermissions: current,
		})
		if err != nil {
			return fmt.Errorf("revoke old rules: %w", err)
		}
	}

	_, err = c.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String(sgID),
		IpPermissions: baseIngressRules(),
	})
	if err != nil {
		return fmt.Errorf("authorize rules: %w", err)
	}
	return nil
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

// baseIngressRules returns base rules (SSH + internal). No HTTP ports.
func baseIngressRules() []ec2types.IpPermission {
	pub := []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}
	priv := []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16")}}

	return []ec2types.IpPermission{
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(22), ToPort: aws.Int32(22), IpRanges: pub},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(6443), ToPort: aws.Int32(6443), IpRanges: priv},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(10250), ToPort: aws.Int32(10250), IpRanges: priv},
		{IpProtocol: aws.String("udp"), FromPort: aws.Int32(8472), ToPort: aws.Int32(8472), IpRanges: priv},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(5000), ToPort: aws.Int32(5000), IpRanges: priv},
	}
}

// buildIngressRules builds the full AWS rule set from base + allowed public ports.
func buildIngressRules(allowed provider.PortAllowList) []ec2types.IpPermission {
	priv := []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16")}}

	// Internal ports — always present
	rules := []ec2types.IpPermission{
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(6443), ToPort: aws.Int32(6443), IpRanges: priv},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(10250), ToPort: aws.Int32(10250), IpRanges: priv},
		{IpProtocol: aws.String("udp"), FromPort: aws.Int32(8472), ToPort: aws.Int32(8472), IpRanges: priv},
		{IpProtocol: aws.String("tcp"), FromPort: aws.Int32(5000), ToPort: aws.Int32(5000), IpRanges: priv},
	}

	// SSH — defaults to open, overridable
	sshRanges := []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}
	if ips, ok := allowed["22"]; ok && len(ips) > 0 {
		sshRanges = cidrsToIPRanges(ips)
	}
	rules = append(rules, ec2types.IpPermission{
		IpProtocol: aws.String("tcp"), FromPort: aws.Int32(22), ToPort: aws.Int32(22), IpRanges: sshRanges,
	})

	// Public + custom ports from allow list
	for _, port := range provider.SortedPorts(allowed) {
		if port == "22" || provider.IsInternalPort(port) {
			continue
		}
		if ips := allowed[port]; len(ips) > 0 {
			p := parsePort32(port)
			rules = append(rules, ec2types.IpPermission{
				IpProtocol: aws.String("tcp"), FromPort: aws.Int32(p), ToPort: aws.Int32(p),
				IpRanges: cidrsToIPRanges(ips),
			})
		}
	}

	return rules
}

// ReconcileFirewallRules replaces all ingress rules on the named security group
// with the desired set built from the allow list + internal rules.
func (c *Client) ReconcileFirewallRules(ctx context.Context, name string, allowed provider.PortAllowList) error {
	sg, err := c.findSecurityGroupByName(ctx, name, "")
	if err != nil {
		return fmt.Errorf("find security group: %w", err)
	}
	if sg == nil {
		return fmt.Errorf("security group %q not found", name)
	}
	sgID := deref(sg.GroupId)

	// Revoke current rules
	resp, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	if err != nil {
		return fmt.Errorf("describe security group: %w", err)
	}
	if len(resp.SecurityGroups) > 0 {
		current := resp.SecurityGroups[0].IpPermissions
		if len(current) > 0 {
			_, err = c.ec2.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
				GroupId:       aws.String(sgID),
				IpPermissions: current,
			})
			if err != nil {
				return fmt.Errorf("revoke old rules: %w", err)
			}
		}
	}

	// Authorize desired rules
	_, err = c.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String(sgID),
		IpPermissions: buildIngressRules(allowed),
	})
	if err != nil {
		return fmt.Errorf("authorize rules: %w", err)
	}
	return nil
}

// GetFirewallRules returns the current public port rules on the named security group.
func (c *Client) GetFirewallRules(ctx context.Context, name string) (provider.PortAllowList, error) {
	sg, err := c.findSecurityGroupByName(ctx, name, "")
	if err != nil {
		return nil, fmt.Errorf("find security group: %w", err)
	}
	if sg == nil {
		return nil, fmt.Errorf("security group %q not found", name)
	}

	resp, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{deref(sg.GroupId)},
	})
	if err != nil {
		return nil, fmt.Errorf("describe security group: %w", err)
	}
	if len(resp.SecurityGroups) == 0 {
		return nil, nil
	}

	result := provider.PortAllowList{}
	for _, perm := range resp.SecurityGroups[0].IpPermissions {
		port := fmt.Sprintf("%d", deref32(perm.FromPort))
		if provider.IsInternalPort(port) {
			continue
		}
		var cidrs []string
		for _, r := range perm.IpRanges {
			cidrs = append(cidrs, deref(r.CidrIp))
		}
		if len(cidrs) > 0 {
			result[port] = cidrs
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// ── Firewall helpers ──────────────────────────────────────────────────────────

func cidrsToIPRanges(cidrs []string) []ec2types.IpRange {
	ranges := make([]ec2types.IpRange, len(cidrs))
	for i, cidr := range cidrs {
		ranges[i] = ec2types.IpRange{CidrIp: aws.String(cidr)}
	}
	return ranges
}

func parsePort32(port string) int32 {
	var p int32
	fmt.Sscanf(port, "%d", &p)
	return p
}
