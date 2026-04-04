package hetzner

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/getnvoi/nvoi/pkg/provider"
)

const defaultBaseURL = "https://api.hetzner.cloud/v1"

type Client struct {
	api   *utils.HTTPClient
	token string
}

func New(token string) *Client {
	return &Client{
		token: token,
		api: &utils.HTTPClient{
			BaseURL: defaultBaseURL,
			SetAuth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) },
			Label:   "hetzner",
		},
	}
}

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
