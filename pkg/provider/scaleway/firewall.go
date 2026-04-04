package scaleway

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Scaleway security groups implement the firewall abstraction.

// ensureFirewall creates or finds a security group by name. Returns the ID.
func (c *Client) ensureFirewall(ctx context.Context, name string, labels map[string]string) (string, error) {
	if name == "" {
		return "", nil
	}
	existing, err := c.getFirewallByName(ctx, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
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
	for _, rule := range defaultFirewallRules() {
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
	if err != nil || fw == nil {
		return nil
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

func defaultFirewallRules() []firewallRule {
	pub := []string{"0.0.0.0/0"}
	priv := []string{"10.0.0.0/8"}
	return []firewallRule{
		{Protocol: "tcp", Port: "22", SourceIPs: pub},
		{Protocol: "tcp", Port: "80", SourceIPs: pub},
		{Protocol: "tcp", Port: "443", SourceIPs: pub},
		{Protocol: "tcp", Port: "6443", SourceIPs: priv},
		{Protocol: "tcp", Port: "10250", SourceIPs: priv},
		{Protocol: "udp", Port: "8472", SourceIPs: priv},
		{Protocol: "tcp", Port: "5000", SourceIPs: priv},
	}
}
