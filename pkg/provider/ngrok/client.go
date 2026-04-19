package ngrok

import (
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
)

const baseURL = "https://api.ngrok.com"

// Client manages ngrok reserved domains via the ngrok API.
type Client struct {
	api       *utils.HTTPClient
	authtoken string
}

// NewClient creates an ngrok tunnel provider from pre-resolved credentials.
func NewClient(creds map[string]string) *Client {
	apiKey := creds["api_key"]
	return &Client{
		api: &utils.HTTPClient{
			BaseURL: baseURL,
			SetAuth: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+apiKey)
				r.Header.Set("Ngrok-Version", "2")
			},
			Label: "ngrok tunnel",
		},
		authtoken: creds["authtoken"],
	}
}

// APIClient returns the underlying HTTP client. Tests override BaseURL via this.
func (c *Client) APIClient() *utils.HTTPClient { return c.api }
