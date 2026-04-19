package cloudflare

import (
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
)

const baseURL = "https://api.cloudflare.com/client/v4"

// Client manages Cloudflare Tunnels via the CF API.
type Client struct {
	api       *utils.HTTPClient
	accountID string
}

// NewClient creates a CF Tunnel provider from pre-resolved credentials.
func NewClient(creds map[string]string) *Client {
	apiToken := creds["api_token"]
	return &Client{
		api: &utils.HTTPClient{
			BaseURL: baseURL,
			SetAuth: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+apiToken)
			},
			Label: "cloudflare tunnel",
		},
		accountID: creds["account_id"],
	}
}

// APIClient returns the underlying HTTP client. Tests override BaseURL via this.
func (c *Client) APIClient() *utils.HTTPClient { return c.api }
