package hetzner

import (
	"context"
	"fmt"
	"strconv"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

type fwRule struct {
	Direction string   `json:"direction"`
	Protocol  string   `json:"protocol"`
	Port      string   `json:"port"`
	SourceIPs []string `json:"source_ips"`
}

func (c *Client) ensureFirewall(ctx context.Context, name string, labels map[string]string) (string, error) {
	// Find existing
	var listResp struct {
		Firewalls []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/firewalls?name=%s", name), nil, &listResp); err != nil {
		return "", fmt.Errorf("get firewall: %w", err)
	}
	for _, fw := range listResp.Firewalls {
		if fw.Name == name {
			id := strconv.FormatInt(fw.ID, 10)
			// Update rules
			_ = c.api.Do(ctx, "POST", fmt.Sprintf("/firewalls/%s/actions/set_rules", id), map[string]any{"rules": defaultFirewallRules()}, nil)
			return id, nil
		}
	}

	// Create
	body := map[string]any{
		"name":   name,
		"labels": labels,
		"rules":  defaultFirewallRules(),
	}
	var createResp struct {
		Firewall struct {
			ID int64 `json:"id"`
		} `json:"firewall"`
	}
	if err := c.api.Do(ctx, "POST", "/firewalls", body, &createResp); err != nil {
		return "", fmt.Errorf("create firewall: %w", err)
	}
	return strconv.FormatInt(createResp.Firewall.ID, 10), nil
}

func (c *Client) deleteFirewall(ctx context.Context, name string) error {
	var resp struct {
		Firewalls []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/firewalls?name=%s", name), nil, &resp); err != nil {
		return err
	}
	found := false
	for _, fw := range resp.Firewalls {
		if fw.Name == name {
			found = true
			err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/firewalls/%s", strconv.FormatInt(fw.ID, 10)), nil, nil)
			if err != nil && !utils.IsNotFound(err) {
				return err
			}
		}
	}
	if !found {
		return utils.ErrNotFound
	}
	return nil
}

func (c *Client) detachFirewall(ctx context.Context, firewallID, serverID string) error {
	intID, _ := strconv.ParseInt(serverID, 10, 64)
	body := map[string]any{
		"remove_from": []map[string]any{
			{"type": "server", "server": map[string]any{"id": intID}},
		},
	}
	return c.api.Do(ctx, "POST", fmt.Sprintf("/firewalls/%s/actions/remove_from_resources", firewallID), body, nil)
}

func (c *Client) ListAllFirewalls(ctx context.Context) ([]*provider.Firewall, error) {
	var resp struct {
		Firewalls []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", "/firewalls?per_page=50", nil, &resp); err != nil {
		return nil, fmt.Errorf("list firewalls: %w", err)
	}
	out := make([]*provider.Firewall, 0, len(resp.Firewalls))
	for _, fw := range resp.Firewalls {
		out = append(out, &provider.Firewall{ID: strconv.FormatInt(fw.ID, 10), Name: fw.Name})
	}
	return out, nil
}

// defaultFirewallRules returns the standard nvoi firewall rules.
func defaultFirewallRules() []fwRule {
	pub := []string{"0.0.0.0/0", "::/0"}
	priv := []string{utils.PrivateNetworkCIDR}
	return []fwRule{
		{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: pub},
		{Direction: "in", Protocol: "tcp", Port: "80", SourceIPs: pub},
		{Direction: "in", Protocol: "tcp", Port: "443", SourceIPs: pub},
		{Direction: "in", Protocol: "tcp", Port: "6443", SourceIPs: priv},
		{Direction: "in", Protocol: "tcp", Port: "10250", SourceIPs: priv},
		{Direction: "in", Protocol: "udp", Port: "8472", SourceIPs: priv},
		{Direction: "in", Protocol: "tcp", Port: "5000", SourceIPs: priv},
	}
}
