// Package hetzner implements the compute provider for Hetzner Cloud.
package hetzner

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/hetznerbase"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type Client struct {
	api   *utils.HTTPClient
	token string
}

func New(token string) *Client {
	return &Client{
		token: token,
		api:   hetznerbase.NewAPI(token),
	}
}

// APIClient returns the underlying HTTP client for tests to override BaseURL.
// Production callers must not depend on this accessor.
func (c *Client) APIClient() *utils.HTTPClient { return c.api }

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.token == "" {
		return fmt.Errorf("hetzner: HETZNER_TOKEN is required")
	}
	var resp struct {
		Datacenters []any `json:"datacenters"`
	}
	return c.api.Do(ctx, "GET", "/datacenters?per_page=1", nil, &resp)
}

func (c *Client) ArchForType(t string) string {
	if len(t) >= 3 && strings.ToLower(t[:3]) == "cax" {
		return "arm64"
	}
	return "amd64"
}

var _ provider.ComputeProvider = (*Client)(nil)
