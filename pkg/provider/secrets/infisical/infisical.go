// Package infisical implements SecretsProvider using the Infisical API.
// Supports both Infisical Cloud and self-hosted instances.
package infisical

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

const defaultHost = "https://app.infisical.com"

// Client manages secrets via the Infisical API.
type Client struct {
	api         *utils.HTTPClient
	projectID   string
	environment string
}

func New(creds map[string]string) *Client {
	host := creds["host"]
	if host == "" {
		host = defaultHost
	}
	token := creds["token"]
	env := creds["environment"]
	if env == "" {
		env = "production"
	}
	return &Client{
		api: &utils.HTTPClient{
			BaseURL: host + "/api",
			SetAuth: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+token)
			},
			Label: "infisical",
		},
		projectID:   creds["project_id"],
		environment: env,
	}
}

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.projectID == "" {
		return fmt.Errorf("infisical: project_id is required")
	}
	_, err := c.List(ctx)
	if err != nil {
		return fmt.Errorf("infisical: validate credentials: %w", err)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, key string) (string, error) {
	var resp struct {
		Secret struct {
			SecretValue string `json:"secretValue"`
		} `json:"secret"`
	}
	path := fmt.Sprintf("/v3/secrets/raw/%s?workspaceId=%s&environment=%s", key, c.projectID, c.environment)
	if err := c.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return "", fmt.Errorf("infisical: get %q: %w", key, err)
	}
	return resp.Secret.SecretValue, nil
}

func (c *Client) Set(ctx context.Context, key, value string) error {
	body := map[string]any{
		"workspaceId": c.projectID,
		"environment": c.environment,
		"secretName":  key,
		"secretValue": value,
	}
	// Try create; on conflict update.
	err := c.api.Do(ctx, "POST", "/v3/secrets/raw", body, nil)
	if err != nil {
		// Update existing.
		path := fmt.Sprintf("/v3/secrets/raw/%s", key)
		updateErr := c.api.Do(ctx, "PATCH", path, body, nil)
		if updateErr != nil {
			return fmt.Errorf("infisical: set %q: create failed: %w, update failed: %w", key, err, updateErr)
		}
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	body := map[string]any{
		"workspaceId": c.projectID,
		"environment": c.environment,
		"secretName":  key,
	}
	path := fmt.Sprintf("/v3/secrets/raw/%s", key)
	if err := c.api.Do(ctx, "DELETE", path, body, nil); err != nil {
		return fmt.Errorf("infisical: delete %q: %w", key, err)
	}
	return nil
}

func (c *Client) List(ctx context.Context) ([]string, error) {
	var resp struct {
		Secrets []struct {
			SecretKey string `json:"secretKey"`
		} `json:"secrets"`
	}
	path := fmt.Sprintf("/v3/secrets/raw?workspaceId=%s&environment=%s", c.projectID, c.environment)
	if err := c.api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("infisical: list: %w", err)
	}
	names := make([]string, len(resp.Secrets))
	for i, s := range resp.Secrets {
		names[i] = s.SecretKey
	}
	return names, nil
}

var _ provider.SecretsProvider = (*Client)(nil)
