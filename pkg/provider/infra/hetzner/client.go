// Package hetzner implements the infra provider for Hetzner Cloud.
package hetzner

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider/hetznerbase"
	"github.com/getnvoi/nvoi/pkg/utils"
)

type Client struct {
	api   *utils.HTTPClient
	token string

	// shell caches the SSH connection Bootstrap (or NodeShell's cold path)
	// dials to the master, so repeat NodeShell calls return the same
	// connection and Close() can release it. Access via cachedShell /
	// setCachedShell which take hetznerCacheMu.
	shell utils.SSHClient
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

// Compile-time satisfaction lives in infra.go (var _ provider.InfraProvider).
