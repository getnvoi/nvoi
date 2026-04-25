package hetzner

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/infra"
	"github.com/getnvoi/nvoi/pkg/utils"
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
			return strconv.FormatInt(fw.ID, 10), nil
		}
	}

	// Create
	body := map[string]any{
		"name":   name,
		"labels": labels,
		"rules":  baseFirewallRules(),
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

func (c *Client) DeleteFirewall(ctx context.Context, name string) error {
	var resp struct {
		Firewalls []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/firewalls?name=%s", name), nil, &resp); err != nil {
		return err
	}
	for _, fw := range resp.Firewalls {
		if fw.Name == name {
			err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/firewalls/%s", strconv.FormatInt(fw.ID, 10)), nil, nil)
			if err != nil && !utils.IsNotFound(err) {
				return err
			}
		}
	}
	return nil // idempotent — not found is fine
}

func (c *Client) detachFirewall(ctx context.Context, firewallID, serverID string) error {
	intID, _ := strconv.ParseInt(serverID, 10, 64)
	body := map[string]any{
		"remove_from": []map[string]any{
			{"type": "server", "server": map[string]any{"id": intID}},
		},
	}
	return utils.Poll(ctx, infra.PollInterval, infra.PollFast, func() (bool, error) {
		var resp struct {
			Actions []struct {
				ID int64 `json:"id"`
			} `json:"actions"`
		}
		err := c.api.Do(ctx, "POST", fmt.Sprintf("/firewalls/%s/actions/remove_from_resources", firewallID), body, &resp)
		switch {
		case err == nil:
			// fall through
		case errors.Is(err, utils.ErrNotFound):
			return true, nil // firewall or attachment already gone
		case errors.Is(err, infra.ErrLocked):
			return false, nil // retry
		default:
			return false, fmt.Errorf("detach firewall: %w", err)
		}
		for _, a := range resp.Actions {
			if a.ID != 0 {
				if err := c.waitForAction(ctx, a.ID); err != nil {
					return false, fmt.Errorf("detach firewall action: %w", err)
				}
			}
		}
		return true, nil
	})
}

func (c *Client) attachFirewall(ctx context.Context, firewallID, serverID string) error {
	intID, _ := strconv.ParseInt(serverID, 10, 64)
	body := map[string]any{
		"apply_to": []map[string]any{
			{"type": "server", "server": map[string]any{"id": intID}},
		},
	}
	var resp struct {
		Actions []struct {
			ID int64 `json:"id"`
		} `json:"actions"`
	}
	if err := c.api.Do(ctx, "POST", fmt.Sprintf("/firewalls/%s/actions/apply_to_resources", firewallID), body, &resp); err != nil {
		if errors.Is(err, infra.ErrAlreadyAttached) {
			return nil // idempotent
		}
		return fmt.Errorf("attach firewall: %w", err)
	}
	for _, a := range resp.Actions {
		if a.ID != 0 {
			if err := c.waitForAction(ctx, a.ID); err != nil {
				return fmt.Errorf("attach firewall action: %w", err)
			}
		}
	}
	return nil
}

// reconcileServerFirewall ensures the server is attached to the desired firewall.
// Detaches any other firewalls, attaches the correct one.
func (c *Client) reconcileServerFirewall(ctx context.Context, serverID, desiredFWName string, labels map[string]string) error {
	desiredFWID, err := c.ensureFirewall(ctx, desiredFWName, labels)
	if err != nil {
		return err
	}

	currentFWIDs, _, err := c.getServerAttachments(ctx, serverID)
	if err != nil {
		return err
	}

	// Check if already correct
	hasDesired := false
	for _, fwID := range currentFWIDs {
		if fwID == desiredFWID {
			hasDesired = true
		}
	}
	if hasDesired && len(currentFWIDs) == 1 {
		return nil // already on the right firewall, nothing else attached
	}

	// Detach wrong firewalls
	for _, fwID := range currentFWIDs {
		if fwID != desiredFWID {
			if err := c.detachFirewall(ctx, fwID, serverID); err != nil {
				return err
			}
		}
	}

	// Attach correct firewall if not already
	if !hasDesired {
		if err := c.attachFirewall(ctx, desiredFWID, serverID); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) ListAllFirewalls(ctx context.Context) ([]*provider.Firewall, error) {
	var resp struct {
		Firewalls []struct {
			ID     int64             `json:"id"`
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", "/firewalls?per_page=50", nil, &resp); err != nil {
		return nil, fmt.Errorf("list firewalls: %w", err)
	}
	out := make([]*provider.Firewall, 0, len(resp.Firewalls))
	for _, fw := range resp.Firewalls {
		out = append(out, &provider.Firewall{ID: strconv.FormatInt(fw.ID, 10), Name: fw.Name, Labels: fw.Labels})
	}
	return out, nil
}

// baseFirewallRules returns the base nvoi firewall rules (SSH + internal).
// HTTP ports (80, 443) are NOT included — managed by firewall set.
func baseFirewallRules() []fwRule {
	pub := []string{"0.0.0.0/0", "::/0"}
	priv := []string{utils.PrivateNetworkCIDR}
	return []fwRule{
		{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: pub},
		{Direction: "in", Protocol: "tcp", Port: "6443", SourceIPs: priv},
		{Direction: "in", Protocol: "tcp", Port: "10250", SourceIPs: priv},
		{Direction: "in", Protocol: "udp", Port: "8472", SourceIPs: priv},
		{Direction: "in", Protocol: "tcp", Port: "5000", SourceIPs: priv},
	}
}

// buildFirewallRules builds the full rule set from base rules + allowed public ports.
func buildFirewallRules(allowed provider.PortAllowList) []fwRule {
	priv := []string{utils.PrivateNetworkCIDR}

	// Internal ports — always present
	rules := []fwRule{
		{Direction: "in", Protocol: "tcp", Port: "6443", SourceIPs: priv},
		{Direction: "in", Protocol: "tcp", Port: "10250", SourceIPs: priv},
		{Direction: "in", Protocol: "udp", Port: "8472", SourceIPs: priv},
		{Direction: "in", Protocol: "tcp", Port: "5000", SourceIPs: priv},
	}

	// SSH — defaults to open, overridable
	sshCIDRs := []string{"0.0.0.0/0", "::/0"}
	if ips, ok := allowed["22"]; ok && len(ips) > 0 {
		sshCIDRs = ips
	}
	rules = append(rules, fwRule{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: sshCIDRs})

	// Public + custom ports from allow list
	for _, port := range provider.SortedPorts(allowed) {
		if port == "22" || provider.IsInternalPort(port) {
			continue
		}
		if ips := allowed[port]; len(ips) > 0 {
			rules = append(rules, fwRule{Direction: "in", Protocol: "tcp", Port: port, SourceIPs: ips})
		}
	}

	return rules
}

// ReconcileFirewallRules replaces all rules on the named firewall with the
// desired set built from the allow list + base internal rules.
func (c *Client) ReconcileFirewallRules(ctx context.Context, name string, allowed provider.PortAllowList) error {
	var listResp struct {
		Firewalls []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/firewalls?name=%s", name), nil, &listResp); err != nil {
		return fmt.Errorf("find firewall: %w", err)
	}
	for _, fw := range listResp.Firewalls {
		if fw.Name == name {
			id := strconv.FormatInt(fw.ID, 10)
			rules := buildFirewallRules(allowed)
			return c.api.Do(ctx, "POST", fmt.Sprintf("/firewalls/%s/actions/set_rules", id), map[string]any{"rules": rules}, nil)
		}
	}
	return utils.ErrNotFound
}

// GetFirewallRules returns the current public port rules on the named firewall.
func (c *Client) GetFirewallRules(ctx context.Context, name string) (provider.PortAllowList, error) {
	var listResp struct {
		Firewalls []struct {
			ID    int64    `json:"id"`
			Name  string   `json:"name"`
			Rules []fwRule `json:"rules"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/firewalls?name=%s", name), nil, &listResp); err != nil {
		return nil, fmt.Errorf("find firewall: %w", err)
	}
	for _, fw := range listResp.Firewalls {
		if fw.Name == name {
			result := provider.PortAllowList{}
			for _, rule := range fw.Rules {
				if rule.Direction != "in" {
					continue
				}
				if provider.IsInternalPort(rule.Port) {
					continue
				}
				result[rule.Port] = rule.SourceIPs
			}
			if len(result) == 0 {
				return nil, nil
			}
			return result, nil
		}
	}
	return nil, utils.ErrNotFound
}
