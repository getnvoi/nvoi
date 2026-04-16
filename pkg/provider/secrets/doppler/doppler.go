// Package doppler implements SecretsProvider using the Doppler REST API.
package doppler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

const baseURL = "https://api.doppler.com/v3"

// Client manages secrets via the Doppler API.
type Client struct {
	api     *utils.HTTPClient
	project string
	config  string
}

func New(creds map[string]string) *Client {
	token := creds["token"]
	return &Client{
		api: &utils.HTTPClient{
			BaseURL: baseURL,
			SetAuth: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+token)
			},
			Label: "doppler",
		},
		project: creds["project"],
		config:  creds["config"],
	}
}

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.project == "" || c.config == "" {
		return fmt.Errorf("doppler: project and config are required")
	}
	// List secrets to verify the token is valid.
	_, err := c.List(ctx)
	if err != nil {
		return fmt.Errorf("doppler: validate credentials: %w", err)
	}
	return nil
}

// Get returns the value for a secret key. Returns ("", nil) if the key
// does not exist — honoring the CredentialSource contract. Only real
// failures (auth, network) are returned as errors.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	var resp struct {
		Value struct {
			Raw string `json:"raw"`
		} `json:"value"`
	}
	path := fmt.Sprintf("/configs/config/secret?project=%s&config=%s&name=%s", c.project, c.config, key)
	if err := c.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		if utils.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("doppler: get %q: %w", key, err)
	}
	return resp.Value.Raw, nil
}

func (c *Client) List(ctx context.Context) ([]string, error) {
	var resp struct {
		Secrets map[string]any `json:"secrets"`
	}
	path := fmt.Sprintf("/configs/config/secrets?project=%s&config=%s", c.project, c.config)
	if err := c.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("doppler: list: %w", err)
	}
	names := make([]string, 0, len(resp.Secrets))
	for k := range resp.Secrets {
		names = append(names, k)
	}
	return names, nil
}

var _ provider.SecretsProvider = (*Client)(nil)
