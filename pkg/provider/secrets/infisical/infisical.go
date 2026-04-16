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

// Client manages secrets via the Infisical API using Universal Auth.
type Client struct {
	api          *utils.HTTPClient
	host         string
	clientID     string
	clientSecret string
	projectSlug  string
	environment  string
}

func New(creds map[string]string) *Client {
	host := creds["host"]
	if host == "" {
		host = defaultHost
	}
	env := creds["environment"]
	if env == "" {
		env = "production"
	}
	return &Client{
		api: &utils.HTTPClient{
			BaseURL: host + "/api",
			Label:   "infisical",
		},
		host:         host,
		clientID:     creds["client_id"],
		clientSecret: creds["client_secret"],
		projectSlug:  creds["project_slug"],
		environment:  env,
	}
}

// authenticate obtains an access token via Universal Auth.
func (c *Client) authenticate(ctx context.Context) (string, error) {
	var resp struct {
		AccessToken string `json:"accessToken"`
	}
	body := map[string]string{
		"clientId":     c.clientID,
		"clientSecret": c.clientSecret,
	}
	if err := c.api.Do(ctx, "POST", "/v1/auth/universal-auth/login", body, &resp); err != nil {
		return "", fmt.Errorf("infisical: authenticate: %w", err)
	}
	return resp.AccessToken, nil
}

// authedAPI returns an HTTPClient with a fresh access token.
func (c *Client) authedAPI(ctx context.Context) (*utils.HTTPClient, error) {
	token, err := c.authenticate(ctx)
	if err != nil {
		return nil, err
	}
	return &utils.HTTPClient{
		BaseURL: c.host + "/api",
		SetAuth: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+token)
		},
		Label: "infisical",
	}, nil
}

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.projectSlug == "" {
		return fmt.Errorf("infisical: project_slug is required")
	}
	_, err := c.List(ctx)
	if err != nil {
		return fmt.Errorf("infisical: validate credentials: %w", err)
	}
	return nil
}

// Get returns the value for a secret key. Returns ("", nil) if the key
// does not exist — honoring the CredentialSource contract. Only real
// failures (auth, network) are returned as errors.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	api, err := c.authedAPI(ctx)
	if err != nil {
		return "", err
	}
	var resp struct {
		Secret struct {
			SecretValue string `json:"secretValue"`
		} `json:"secret"`
	}
	path := fmt.Sprintf("/v3/secrets/raw/%s?workspaceSlug=%s&environment=%s", key, c.projectSlug, c.environment)
	if err := api.Do(ctx, "GET", path, nil, &resp); err != nil {
		if utils.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("infisical: get %q: %w", key, err)
	}
	return resp.Secret.SecretValue, nil
}

func (c *Client) Set(ctx context.Context, key, value string) error {
	api, err := c.authedAPI(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{
		"workspaceSlug": c.projectSlug,
		"environment":   c.environment,
		"secretName":    key,
		"secretValue":   value,
	}
	err = api.Do(ctx, "POST", "/v3/secrets/raw", body, nil)
	if err != nil {
		// Only retry as update if the create failed due to conflict (already exists).
		// Auth errors, network errors, etc. should not be retried.
		if !utils.IsConflict(err) {
			return fmt.Errorf("infisical: set %q: %w", key, err)
		}
		path := fmt.Sprintf("/v3/secrets/raw/%s", key)
		if updateErr := api.Do(ctx, "PATCH", path, body, nil); updateErr != nil {
			return fmt.Errorf("infisical: set %q: create conflict, update failed: %w", key, updateErr)
		}
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	api, err := c.authedAPI(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{
		"workspaceSlug": c.projectSlug,
		"environment":   c.environment,
		"secretName":    key,
	}
	path := fmt.Sprintf("/v3/secrets/raw/%s", key)
	if err := api.Do(ctx, "DELETE", path, body, nil); err != nil {
		return fmt.Errorf("infisical: delete %q: %w", key, err)
	}
	return nil
}

func (c *Client) List(ctx context.Context) ([]string, error) {
	api, err := c.authedAPI(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Secrets []struct {
			SecretKey string `json:"secretKey"`
		} `json:"secrets"`
	}
	path := fmt.Sprintf("/v3/secrets/raw?workspaceSlug=%s&environment=%s", c.projectSlug, c.environment)
	if err := api.Do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("infisical: list: %w", err)
	}
	names := make([]string, len(resp.Secrets))
	for i, s := range resp.Secrets {
		names[i] = s.SecretKey
	}
	return names, nil
}

var _ provider.SecretsProvider = (*Client)(nil)
