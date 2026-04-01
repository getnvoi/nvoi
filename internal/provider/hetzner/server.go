package hetzner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/provider"
)

type serverJSON struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	PublicNet struct {
		IPv4 struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
		IPv6 struct {
			IP string `json:"ip"`
		} `json:"ipv6"`
	} `json:"public_net"`
	PrivateNet []struct {
		IP string `json:"ip"`
	} `json:"private_net"`
}

func serverFrom(s serverJSON) *provider.Server {
	srv := &provider.Server{
		ID: strconv.FormatInt(s.ID, 10), Name: s.Name, Status: s.Status,
		IPv4: s.PublicNet.IPv4.IP, IPv6: s.PublicNet.IPv6.IP,
	}
	if len(s.PrivateNet) > 0 {
		srv.PrivateIP = s.PrivateNet[0].IP
	}
	return srv
}

// EnsureServer creates or returns existing server.
// Firewall and network are resolved internally by naming convention.
func (c *Client) EnsureServer(ctx context.Context, req provider.CreateServerRequest) (*provider.Server, error) {
	// Check existing
	existing, err := c.getServerByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// TODO: validate labels match req.Labels to prevent cross-project collision
		// on the same Hetzner account (different app, same naming by accident).
		fmt.Printf("  server %s exists (%s)\n", req.Name, existing.IPv4)
		return existing, nil
	}

	fwName := req.FirewallName
	netName := req.NetworkName

	fwID, err := c.ensureFirewall(ctx, fwName, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("firewall: %w", err)
	}
	fmt.Printf("  ✓ firewall %s\n", fwName)

	netID, err := c.ensureNetwork(ctx, netName, req.Location, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}
	fmt.Printf("  ✓ network %s\n", netName)

	// Create server
	fwInt, _ := strconv.ParseInt(fwID, 10, 64)
	netInt, _ := strconv.ParseInt(netID, 10, 64)

	body := map[string]any{
		"name":        req.Name,
		"server_type": req.ServerType,
		"image":       req.Image,
		"location":    req.Location,
		"user_data":   req.UserData,
		"labels":      req.Labels,
		"firewalls":   []map[string]any{{"firewall": fwInt}},
		"networks":    []int64{netInt},
	}

	var resp struct {
		Server serverJSON `json:"server"`
	}
	if err := c.api.Do(ctx, "POST", "/servers", body, &resp); err != nil {
		return nil, fmt.Errorf("create server: %w", err)
	}

	fmt.Printf("  ✓ server %s created (%s)\n", req.Name, serverFrom(resp.Server).IPv4)
	return serverFrom(resp.Server), nil
}

func (c *Client) DeleteServer(ctx context.Context, req provider.DeleteServerRequest) error {
	srv, err := c.getServerByName(ctx, req.Name)
	if err != nil {
		return err
	}
	if srv == nil {
		return nil // already gone
	}

	// Detach firewall
	fwName := req.FirewallName
	var fwResp struct {
		Firewalls []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firewalls"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/firewalls?name=%s", fwName), nil, &fwResp); err == nil {
		for _, fw := range fwResp.Firewalls {
			if fw.Name == fwName {
				_ = c.detachFirewall(ctx, strconv.FormatInt(fw.ID, 10), srv.ID)
			}
		}
	}

	// Delete server
	if err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/servers/%s", srv.ID), nil, nil); err != nil {
		if !core.IsNotFound(err) {
			return fmt.Errorf("delete server: %w", err)
		}
	}

	// Clean up firewall + network
	_ = c.deleteFirewall(ctx, fwName)
	_ = c.deleteNetwork(ctx, req.NetworkName)

	return nil
}

func (c *Client) ListAllServers(ctx context.Context) ([]*provider.Server, error) {
	return c.ListServers(ctx, nil)
}

func (c *Client) ListServers(ctx context.Context, labels map[string]string) ([]*provider.Server, error) {
	var parts []string
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	selector := ""
	if len(parts) > 0 {
		selector = "&label_selector=" + strings.Join(parts, ",")
	}

	var resp struct {
		Servers []serverJSON `json:"servers"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/servers?per_page=50%s", selector), nil, &resp); err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}

	servers := make([]*provider.Server, 0, len(resp.Servers))
	for _, s := range resp.Servers {
		servers = append(servers, serverFrom(s))
	}
	return servers, nil
}

func (c *Client) getServerByName(ctx context.Context, name string) (*provider.Server, error) {
	var resp struct {
		Servers []serverJSON `json:"servers"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/servers?name=%s", name), nil, &resp); err != nil {
		return nil, fmt.Errorf("get server: %w", err)
	}
	for _, s := range resp.Servers {
		if s.Name == name {
			return serverFrom(s), nil
		}
	}
	return nil, nil
}
