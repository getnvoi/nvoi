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
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/s3ops"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// BucketClient manages R2 buckets via Cloudflare API + S3-compatible operations.
type BucketClient struct {
	api                *utils.HTTPClient
	apiKey             string
	accountID          string
	creds              *provider.BucketCredentials
	s3EndpointOverride string // set only by tests; empty = real R2 endpoint
}

// NewBucket creates a Cloudflare R2 bucket provider.
func NewBucket(creds map[string]string) *BucketClient {
	apiKey := creds["api_key"]
	return &BucketClient{
		api:       NewAPI(apiKey, "cloudflare r2"),
		apiKey:    apiKey,
		accountID: creds["account_id"],
	}
}

// APIClient returns the underlying HTTP client for tests to override BaseURL.
// Production callers must not depend on this accessor.
func (c *BucketClient) APIClient() *utils.HTTPClient { return c.api }

// SetS3EndpointOverride replaces the R2 S3-compatible endpoint. Tests only.
// Production callers must not call this.
func (c *BucketClient) SetS3EndpointOverride(url string) {
	c.s3EndpointOverride = url
	c.creds = nil // invalidate cache so next Credentials() rebuilds
}

func (c *BucketClient) ValidateCredentials(ctx context.Context) error {
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

func (c *BucketClient) EnsureBucket(ctx context.Context, name string) error {
	err := c.api.Do(ctx, "POST", fmt.Sprintf("/accounts/%s/r2/buckets", c.accountID), map[string]string{"name": name}, nil)
	if err != nil {
		// 409 = already exists — success
		if apiErr, ok := err.(*utils.APIError); ok && apiErr.HTTPStatus() == 409 {
			return nil
		}
		return fmt.Errorf("create bucket %s: %w", name, err)
	}
	return nil
}

func (c *BucketClient) EmptyBucket(ctx context.Context, name string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3ops.EmptyBucket(ctx, cr, name)
}

func (c *BucketClient) DeleteBucket(ctx context.Context, name string) error {
	err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/accounts/%s/r2/buckets/%s", c.accountID, name), nil, nil)
	if err != nil {
		if utils.IsNotFound(err) {
			return utils.ErrNotFound
		}
		return fmt.Errorf("delete bucket %s: %w", name, err)
	}
	return nil
}

func (c *BucketClient) SetCORS(ctx context.Context, name string, origins, methods []string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3ops.SetCORS(ctx, cr, name, origins, methods)
}

func (c *BucketClient) ClearCORS(ctx context.Context, name string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3ops.ClearCORS(ctx, cr, name)
}

func (c *BucketClient) SetLifecycle(ctx context.Context, name string, expireDays int) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3ops.SetLifecycle(ctx, cr, name, expireDays)
}

// Credentials returns S3-compatible access details derived from the CF API token.
// R2 uses the token ID as access key and SHA-256(token) as secret key.
func (c *BucketClient) Credentials(ctx context.Context) (provider.BucketCredentials, error) {
	if c.creds != nil {
		return *c.creds, nil
	}
	tokenID, err := c.tokenVerify(ctx)
	if err != nil {
		return provider.BucketCredentials{}, fmt.Errorf("cloudflare credentials: %w", err)
	}
	hash := sha256.Sum256([]byte(c.apiKey))
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", c.accountID)
	if c.s3EndpointOverride != "" {
		endpoint = c.s3EndpointOverride
	}
	c.creds = &provider.BucketCredentials{
		Endpoint:        endpoint,
		AccessKeyID:     tokenID,
		SecretAccessKey: hex.EncodeToString(hash[:]),
		Region:          "auto",
	}
	return *c.creds, nil
}

func (c *BucketClient) tokenVerify(ctx context.Context) (string, error) {
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

// ListResources lists every R2 bucket on the account. R2 buckets carry
// no native labels or tags surfaced via the Cloudflare API, so Owned
// is computed from the deterministic `nvoi-` name prefix that
// EnsureBucket stamps on creation. Pre-existing buckets that don't
// match are surfaced with Owned=false.
func (c *BucketClient) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
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
		g.Owned = append(g.Owned, strings.HasPrefix(b.Name, "nvoi-"))
	}
	return []provider.ResourceGroup{g}, nil
}

var _ provider.BucketProvider = (*BucketClient)(nil)
