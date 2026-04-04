// Package cloudflare implements provider.BucketProvider for Cloudflare R2.
// Bucket creation uses the CF REST API. CORS/lifecycle use standard S3 API.
//
// Credentials: CF_API_KEY (CF API token), CF_ACCOUNT_ID (CF account ID)
package cloudflare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

const cfBaseURL = "https://api.cloudflare.com/client/v4"

// Client manages R2 buckets via Cloudflare API + S3-compatible operations.
type Client struct {
	api       *core.HTTPClient
	apiKey    string
	accountID string
	creds     *provider.BucketCredentials
}

// New creates a Cloudflare R2 bucket provider.
func New(creds map[string]string) *Client {
	apiKey := creds["api_key"]
	return &Client{
		api: &core.HTTPClient{
			BaseURL: cfBaseURL,
			SetAuth: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+apiKey)
			},
			Label: "cloudflare r2",
		},
		apiKey:    apiKey,
		accountID: creds["account_id"],
	}
}

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.apiKey == "" {
		return fmt.Errorf("cloudflare r2: api_key is required")
	}
	if c.accountID == "" {
		return fmt.Errorf("cloudflare r2: account_id is required")
	}
	_, err := c.tokenVerify(ctx)
	if err != nil {
		return fmt.Errorf("cloudflare r2: %w", err)
	}
	return nil
}

func (c *Client) EnsureBucket(ctx context.Context, name string) error {
	err := c.api.Do(ctx, "POST", fmt.Sprintf("/accounts/%s/r2/buckets", c.accountID), map[string]string{"name": name}, nil)
	if err != nil {
		// 409 = already exists — success
		if apiErr, ok := err.(*core.APIError); ok && apiErr.HTTPStatus() == 409 {
			return nil
		}
		return fmt.Errorf("create bucket %s: %w", name, err)
	}
	return nil
}

func (c *Client) EmptyBucket(ctx context.Context, name string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3EmptyBucket(ctx, cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name)
}

func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/accounts/%s/r2/buckets/%s", c.accountID, name), nil, nil)
	if err != nil {
		// 404 = already gone — success
		if core.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete bucket %s: %w", name, err)
	}
	return nil
}

func (c *Client) SetCORS(ctx context.Context, name string, origins, methods []string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3SetCORS(ctx, cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name, origins, methods)
}

func (c *Client) ClearCORS(ctx context.Context, name string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3ClearCORS(ctx, cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name)
}

func (c *Client) SetLifecycle(ctx context.Context, name string, expireDays int) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3SetLifecycle(ctx, cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name, expireDays)
}

// Credentials returns S3-compatible access details derived from the CF API token.
// R2 uses the token ID as access key and SHA-256(token) as secret key.
func (c *Client) Credentials(ctx context.Context) (provider.BucketCredentials, error) {
	if c.creds != nil {
		return *c.creds, nil
	}
	tokenID, err := c.tokenVerify(ctx)
	if err != nil {
		return provider.BucketCredentials{}, fmt.Errorf("cloudflare credentials: %w", err)
	}
	hash := sha256.Sum256([]byte(c.apiKey))
	c.creds = &provider.BucketCredentials{
		Endpoint:        fmt.Sprintf("https://%s.r2.cloudflarestorage.com", c.accountID),
		AccessKeyID:     tokenID,
		SecretAccessKey: hex.EncodeToString(hash[:]),
		Region:          "auto",
	}
	return *c.creds, nil
}

func (c *Client) tokenVerify(ctx context.Context) (string, error) {
	var result struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := c.api.Do(ctx, "GET", "/user/tokens/verify", nil, &result); err != nil {
		return "", err
	}
	if result.Result.ID == "" {
		return "", fmt.Errorf("invalid API token")
	}
	return result.Result.ID, nil
}

func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	var resp struct {
		Result struct {
			Buckets []struct {
				Name string `json:"name"`
			} `json:"buckets"`
		} `json:"result"`
	}
	if err := c.api.Do(ctx, "GET", fmt.Sprintf("/accounts/%s/r2/buckets", c.accountID), nil, &resp); err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "R2 Buckets", Columns: []string{"Name"}}
	for _, b := range resp.Result.Buckets {
		g.Rows = append(g.Rows, []string{b.Name})
	}
	return []provider.ResourceGroup{g}, nil
}

var _ provider.BucketProvider = (*Client)(nil)
