package hetzner

import (
	"context"
	"fmt"
	"strconv"

	"github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

var locationToZone = map[string]string{
	"fsn1": "eu-central",
	"nbg1": "eu-central",
	"hel1": "eu-central",
	"ash":  "us-east",
	"hil":  "us-west",
	"sin":  "ap-southeast",
}

func zoneForLocation(loc string) string {
	if z, ok := locationToZone[loc]; ok {
		return z
	}
	return "eu-central"
}

func (c *Client) ensureNetwork(ctx context.Context, name, location string, labels map[string]string) (string, error) {
	// Find existing
	var listResp struct {
		Networks []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"networks"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/networks?name=%s", name), nil, &listResp); err != nil {
		return "", fmt.Errorf("get network: %w", err)
	}
	for _, n := range listResp.Networks {
		if n.Name == name {
			return strconv.FormatInt(n.ID, 10), nil
		}
	}

	// Create
	body := map[string]any{
		"name":     name,
		"ip_range": core.PrivateNetworkCIDR,
		"labels":   labels,
		"subnets": []map[string]any{{
			"type":         "cloud",
			"ip_range":     core.PrivateNetworkSubnet,
			"network_zone": zoneForLocation(location),
		}},
	}
	var createResp struct {
		Network struct {
			ID int64 `json:"id"`
		} `json:"network"`
	}
	if err := c.api.Do(ctx, "POST", "/networks", body, &createResp); err != nil {
		return "", fmt.Errorf("create network: %w", err)
	}
	return strconv.FormatInt(createResp.Network.ID, 10), nil
}

func (c *Client) ListAllNetworks(ctx context.Context) ([]*provider.Network, error) {
	var resp struct {
		Networks []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"networks"`
	}
	if err := c.api.Do(ctx, "GET", "/networks?per_page=50", nil, &resp); err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	out := make([]*provider.Network, 0, len(resp.Networks))
	for _, n := range resp.Networks {
		out = append(out, &provider.Network{ID: strconv.FormatInt(n.ID, 10), Name: n.Name})
	}
	return out, nil
}

func (c *Client) deleteNetwork(ctx context.Context, name string) error {
	var resp struct {
		Networks []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"networks"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/networks?name=%s", name), nil, &resp); err != nil {
		return err
	}
	for _, n := range resp.Networks {
		if n.Name == name {
			err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/networks/%s", strconv.FormatInt(n.ID, 10)), nil, nil)
			if err != nil && !core.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}
