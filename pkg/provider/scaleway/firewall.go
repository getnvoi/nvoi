package scaleway

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Scaleway security groups implement the firewall abstraction.

// ensureFirewall creates or finds a security group by name. Returns the ID.
// When the SG already exists, rules are reconciled to match the desired set.
func (c *Client) ensureFirewall(ctx context.Context, name string, labels map[string]string) (string, error) {
	if name == "" {
		return "", nil
	}
	existing, err := c.getFirewallByName(ctx, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
		if err := c.reconcileFirewallRules(ctx, existing.ID); err != nil {
			return "", fmt.Errorf("reconcile firewall rules: %w", err)
		}
		return existing.ID, nil
	}

	body := struct {
		Name                  string `json:"name"`
		Project               string `json:"project"`
		Stateful              bool   `json:"stateful"`
		InboundDefaultPolicy  string `json:"inbound_default_policy"`
		OutboundDefaultPolicy string `json:"outbound_default_policy"`
	}{
		Name:                  name,
		Project:               c.projectID,
		Stateful:              true,
		InboundDefaultPolicy:  "drop",
		OutboundDefaultPolicy: "accept",
	}

	var resp struct {
		SecurityGroup struct {
			ID string `json:"id"`
		} `json:"security_group"`
	}
	if err := c.doInstance(ctx, "POST", "/security_groups", body, &resp); err != nil {
		return "", fmt.Errorf("create security group: %w", err)
	}

	// Add default nvoi rules
	for _, rule := range baseFirewallRules() {
		if err := c.addSGRule(ctx, resp.SecurityGroup.ID, rule); err != nil {
			return "", fmt.Errorf("add rule to %s: %w", name, err)
		}
	}

	return resp.SecurityGroup.ID, nil
}

func (c *Client) getFirewallByName(ctx context.Context, name string) (*provider.Firewall, error) {
	var resp struct {
		SecurityGroups []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"security_groups"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/security_groups?name=%s&project=%s", name, c.projectID), nil, &resp); err != nil {
		return nil, err
	}
	for _, sg := range resp.SecurityGroups {
		if sg.Name == name {
			return &provider.Firewall{ID: sg.ID, Name: sg.Name}, nil
		}
	}
	return nil, nil
}

func (c *Client) deleteFirewall(ctx context.Context, name string) error {
	fw, err := c.getFirewallByName(ctx, name)
	if err != nil {
		return err
	}
	if fw == nil {
		return utils.ErrNotFound
	}
	err = c.doInstance(ctx, "DELETE", fmt.Sprintf("/security_groups/%s", fw.ID), nil, nil)
	if err != nil && !utils.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Client) ListAllFirewalls(ctx context.Context) ([]*provider.Firewall, error) {
	var resp struct {
		SecurityGroups []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"security_groups"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/security_groups?project=%s&per_page=50", c.projectID), nil, &resp); err != nil {
		return nil, fmt.Errorf("list security groups: %w", err)
	}
	out := make([]*provider.Firewall, 0, len(resp.SecurityGroups))
	for _, sg := range resp.SecurityGroups {
		out = append(out, &provider.Firewall{ID: sg.ID, Name: sg.Name})
	}
	return out, nil
}

// reconcileFirewallRules replaces all rules on an existing security group
// to match the desired nvoi defaults. Deletes current rules, then re-adds.
// Idempotent — same rules in = same rules out.
func (c *Client) reconcileFirewallRules(ctx context.Context, sgID string) error {
	// List existing rules
	var resp struct {
		Rules []struct {
			ID string `json:"id"`
		} `json:"rules"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/security_groups/%s/rules", sgID), nil, &resp); err != nil {
		return fmt.Errorf("list rules: %w", err)
	}

	// Delete all existing rules
	for _, rule := range resp.Rules {
		if err := c.doInstance(ctx, "DELETE", fmt.Sprintf("/security_groups/%s/rules/%s", sgID, rule.ID), nil, nil); err != nil {
			return fmt.Errorf("delete rule %s: %w", rule.ID, err)
		}
	}

	// Re-add desired rules
	for _, rule := range baseFirewallRules() {
		if err := c.addSGRule(ctx, sgID, rule); err != nil {
			return fmt.Errorf("add rule: %w", err)
		}
	}
	return nil
}

// addSGRule adds a single inbound rule. One rule per source IP (Scaleway constraint).
func (c *Client) addSGRule(ctx context.Context, sgID string, rule firewallRule) error {
	portFrom, portTo := parsePort(rule.Port)
	protocol := strings.ToUpper(rule.Protocol)

	for _, ipRange := range rule.SourceIPs {
		if strings.Contains(ipRange, ":") {
			continue // Scaleway SG rules don't support IPv6 ranges
		}
		body := struct {
			Protocol     string `json:"protocol"`
			Direction    string `json:"direction"`
			Action       string `json:"action"`
			IPRange      string `json:"ip_range"`
			DestPortFrom int    `json:"dest_port_from"`
			DestPortTo   int    `json:"dest_port_to,omitempty"`
		}{
			Protocol:     protocol,
			Direction:    "inbound",
			Action:       "accept",
			IPRange:      ipRange,
			DestPortFrom: portFrom,
		}
		if portTo > 0 && portTo != portFrom {
			body.DestPortTo = portTo
		}
		if err := c.doInstance(ctx, "POST", fmt.Sprintf("/security_groups/%s/rules", sgID), body, nil); err != nil {
			return err
		}
	}
	return nil
}

type firewallRule struct {
	Protocol  string
	Port      string
	SourceIPs []string
}

func parsePort(port string) (from, to int) {
	if idx := strings.Index(port, "-"); idx >= 0 {
		fmt.Sscanf(port[:idx], "%d", &from)
		fmt.Sscanf(port[idx+1:], "%d", &to)
		return
	}
	fmt.Sscanf(port, "%d", &from)
	return from, 0
}

// baseFirewallRules returns the base nvoi firewall rules (SSH + internal).
// HTTP ports (80, 443) are NOT included — managed by firewall set.
func baseFirewallRules() []firewallRule {
	pub := []string{"0.0.0.0/0", "::/0"}
	priv := []string{utils.PrivateNetworkCIDR}
	return []firewallRule{
		{Protocol: "tcp", Port: "22", SourceIPs: pub},
		{Protocol: "tcp", Port: "6443", SourceIPs: priv},
		{Protocol: "tcp", Port: "10250", SourceIPs: priv},
		{Protocol: "udp", Port: "8472", SourceIPs: priv},
		{Protocol: "tcp", Port: "5000", SourceIPs: priv},
	}
}

// buildFirewallRules builds the full rule set from base rules + allowed public ports.
func buildScalewayFirewallRules(allowed provider.PortAllowList) []firewallRule {
	priv := []string{utils.PrivateNetworkCIDR}

	// Internal ports — always present
	rules := []firewallRule{
		{Protocol: "tcp", Port: "6443", SourceIPs: priv},
		{Protocol: "tcp", Port: "10250", SourceIPs: priv},
		{Protocol: "udp", Port: "8472", SourceIPs: priv},
		{Protocol: "tcp", Port: "5000", SourceIPs: priv},
	}

	// SSH — defaults to open (IPv4 + IPv6), overridable
	sshCIDRs := []string{"0.0.0.0/0", "::/0"}
	if ips, ok := allowed["22"]; ok && len(ips) > 0 {
		sshCIDRs = ips
	}
	rules = append(rules, firewallRule{Protocol: "tcp", Port: "22", SourceIPs: sshCIDRs})

	// Public + custom ports from allow list
	for _, port := range provider.SortedPorts(allowed) {
		if port == "22" || provider.IsInternalPort(port) {
			continue
		}
		if ips := allowed[port]; len(ips) > 0 {
			rules = append(rules, firewallRule{Protocol: "tcp", Port: port, SourceIPs: ips})
		}
	}

	return rules
}

// ReconcileFirewallRules replaces all rules on the named security group
// with the desired set built from the allow list + internal rules.
func (c *Client) ReconcileFirewallRules(ctx context.Context, name string, allowed provider.PortAllowList) error {
	fw, err := c.getFirewallByName(ctx, name)
	if err != nil {
		return err
	}
	if fw == nil {
		return utils.ErrNotFound
	}

	// Delete existing rules
	var resp struct {
		Rules []struct {
			ID string `json:"id"`
		} `json:"rules"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/security_groups/%s/rules", fw.ID), nil, &resp); err != nil {
		return fmt.Errorf("list rules: %w", err)
	}
	for _, rule := range resp.Rules {
		if err := c.doInstance(ctx, "DELETE", fmt.Sprintf("/security_groups/%s/rules/%s", fw.ID, rule.ID), nil, nil); err != nil {
			return fmt.Errorf("delete rule %s: %w", rule.ID, err)
		}
	}

	// Re-add desired rules
	for _, rule := range buildScalewayFirewallRules(allowed) {
		if err := c.addSGRule(ctx, fw.ID, rule); err != nil {
			return fmt.Errorf("add rule: %w", err)
		}
	}
	return nil
}

// GetFirewallRules returns the current public port rules on the named security group.
func (c *Client) GetFirewallRules(ctx context.Context, name string) (provider.PortAllowList, error) {
	fw, err := c.getFirewallByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if fw == nil {
		return nil, utils.ErrNotFound
	}

	var resp struct {
		Rules []struct {
			Protocol     string `json:"protocol"`
			Direction    string `json:"direction"`
			IPRange      string `json:"ip_range"`
			DestPortFrom int    `json:"dest_port_from"`
		} `json:"rules"`
	}
	if err := c.doInstance(ctx, "GET", fmt.Sprintf("/security_groups/%s/rules", fw.ID), nil, &resp); err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}

	result := provider.PortAllowList{}
	for _, rule := range resp.Rules {
		if rule.Direction != "inbound" {
			continue
		}
		port := fmt.Sprintf("%d", rule.DestPortFrom)
		if provider.IsInternalPort(port) {
			continue
		}
		result[port] = append(result[port], rule.IPRange)
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}
