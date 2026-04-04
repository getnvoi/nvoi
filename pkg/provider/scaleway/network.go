package scaleway

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// ensureNetwork creates or finds a private network by name. Returns the ID.
func (c *Client) ensureNetwork(ctx context.Context, name string, labels map[string]string) (string, error) {
	if name == "" {
		return "", nil
	}
	existing, err := c.getNetworkByName(ctx, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.ID, nil
	}

	body := struct {
		Name      string   `json:"name"`
		ProjectID string   `json:"project_id"`
		Tags      []string `json:"tags,omitempty"`
		Subnets   []string `json:"subnets,omitempty"`
	}{
		Name:      name,
		ProjectID: c.projectID,
		Tags:      labelsToTags(labels),
		Subnets:   []string{utils.PrivateNetworkSubnet},
	}

	var resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.api.Do(ctx, "POST", c.vpcPath("/private-networks"), body, &resp); err != nil {
		return "", fmt.Errorf("create private network: %w", err)
	}
	return resp.ID, nil
}

func (c *Client) getNetworkByName(ctx context.Context, name string) (*provider.Network, error) {
	var resp struct {
		PrivateNetworks []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"private_networks"`
	}
	if err := c.api.Do(ctx, "GET", c.vpcPath(fmt.Sprintf("/private-networks?name=%s&project_id=%s", name, c.projectID)), nil, &resp); err != nil {
		return nil, err
	}
	for _, pn := range resp.PrivateNetworks {
		if pn.Name == name {
			return &provider.Network{ID: pn.ID, Name: pn.Name}, nil
		}
	}
	return nil, nil
}

func (c *Client) deleteNetwork(ctx context.Context, name string) error {
	net, err := c.getNetworkByName(ctx, name)
	if err != nil || net == nil {
		return nil
	}
	err = c.api.Do(ctx, "DELETE", c.vpcPath(fmt.Sprintf("/private-networks/%s", net.ID)), nil, nil)
	if err != nil && !utils.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Client) ListAllNetworks(ctx context.Context) ([]*provider.Network, error) {
	var resp struct {
		PrivateNetworks []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"private_networks"`
	}
	if err := c.api.Do(ctx, "GET", c.vpcPath(fmt.Sprintf("/private-networks?project_id=%s&per_page=50", c.projectID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("list private networks: %w", err)
	}
	out := make([]*provider.Network, 0, len(resp.PrivateNetworks))
	for _, pn := range resp.PrivateNetworks {
		if strings.HasPrefix(pn.Name, "nvoi-") {
			out = append(out, &provider.Network{ID: pn.ID, Name: pn.Name})
		}
	}
	return out, nil
}

// attachPrivateNetwork attaches a private network to a server via private_nics.
// Idempotent — 400 means already attached.
func (c *Client) attachPrivateNetwork(ctx context.Context, serverID, networkID string) error {
	body := struct {
		PrivateNetworkID string `json:"private_network_id"`
	}{PrivateNetworkID: networkID}

	err := c.doInstance(ctx, "POST", fmt.Sprintf("/servers/%s/private_nics", serverID), body, nil)
	if err != nil {
		if apiErr, ok := err.(*utils.APIError); ok && apiErr.Status == 400 {
			return nil // already attached
		}
		return fmt.Errorf("attach private network: %w", err)
	}
	return nil
}
