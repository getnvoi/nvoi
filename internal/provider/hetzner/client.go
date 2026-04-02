package hetzner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/provider"
)

const defaultBaseURL = "https://api.hetzner.cloud/v1"

type Client struct {
	api   *core.HTTPClient
	token string
	w     io.Writer
}

func New(token string) *Client {
	return &Client{
		token: token,
		w:     io.Discard,
		api: &core.HTTPClient{
			BaseURL: defaultBaseURL,
			SetAuth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) },
			Label:   "hetzner",
		},
	}
}

// SetWriter sets the output writer for progress messages.
func (c *Client) SetWriter(w io.Writer) { c.w = w }

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
