package hetzner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/infra"
	"github.com/getnvoi/nvoi/pkg/utils"
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
	ServerType struct {
		Disk int `json:"disk"`
	} `json:"server_type"`
}

func serverFrom(s serverJSON) *provider.Server {
	srv := &provider.Server{
		ID: strconv.FormatInt(s.ID, 10), Name: s.Name, Status: provider.ServerStatus(s.Status),
		IPv4: s.PublicNet.IPv4.IP, IPv6: s.PublicNet.IPv6.IP,
		DiskGB: s.ServerType.Disk,
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
		// Reconcile firewall attachment — server may be on a legacy or wrong firewall
		if err := c.reconcileServerFirewall(ctx, existing.ID, req.FirewallName, req.Labels); err != nil {
			return nil, fmt.Errorf("reconcile firewall: %w", err)
		}
		return existing, nil
	}

	fwName := req.FirewallName
	netName := req.NetworkName

	fwID, err := c.ensureFirewall(ctx, fwName, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("firewall: %w", err)
	}

	netID, err := c.ensureNetwork(ctx, netName, req.Location, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}

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

	return serverFrom(resp.Server), nil
}

func (c *Client) DeleteServer(ctx context.Context, req provider.DeleteServerRequest) error {
	srv, err := c.getServerByName(ctx, req.Name)
	if err != nil {
		return err
	}
	if srv == nil {
		return nil // idempotent — already gone
	}

	// Fetch firewalls + volumes in one API call
	firewallIDs, volumeIDs, err := c.getServerAttachments(ctx, srv.ID)
	if err != nil {
		return fmt.Errorf("get server attachments: %w", err)
	}

	// Detach firewalls
	for _, fwID := range firewallIDs {
		if err := c.detachFirewall(ctx, fwID, srv.ID); err != nil {
			return fmt.Errorf("detach firewall %s: %w", fwID, err)
		}
	}

	// Detach volumes
	for _, volID := range volumeIDs {
		if err := c.detachVolume(ctx, volID); err != nil {
			return fmt.Errorf("detach volume %s: %w", volID, err)
		}
	}

	// Delete server
	if err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/servers/%s", srv.ID), nil, nil); err != nil {
		if !utils.IsNotFound(err) {
			return fmt.Errorf("delete server: %w", err)
		}
	}

	// Wait for gone — only trust s==nil when the API call succeeded.
	// API errors (rate limit, network) must retry, not short-circuit.
	return utils.Poll(ctx, infra.PollInterval, infra.PollFast, func() (bool, error) {
		s, err := c.getServerByName(ctx, req.Name)
		if err != nil {
			return false, nil // transient API error — retry
		}
		return s == nil, nil
	})
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

// getServerAttachments returns firewall IDs and volume IDs attached to the server
// in a single API call. Both live on the same GET /servers/{id} response.
func (c *Client) getServerAttachments(ctx context.Context, serverID string) (firewallIDs, volumeIDs []string, err error) {
	var resp struct {
		Server struct {
			PublicNet struct {
				Firewalls []struct {
					ID int64 `json:"id"`
				} `json:"firewalls"`
			} `json:"public_net"`
			Volumes []int64 `json:"volumes"`
		} `json:"server"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/servers/%s", serverID), nil, &resp); err != nil {
		return nil, nil, err
	}
	for _, fw := range resp.Server.PublicNet.Firewalls {
		firewallIDs = append(firewallIDs, strconv.FormatInt(fw.ID, 10))
	}
	for _, v := range resp.Server.Volumes {
		volumeIDs = append(volumeIDs, strconv.FormatInt(v, 10))
	}
	return firewallIDs, volumeIDs, nil
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
