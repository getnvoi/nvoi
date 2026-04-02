// Package cloudflare implements provider.BucketProvider for Cloudflare R2.
// Bucket creation uses the CF REST API. CORS/lifecycle use standard S3 API.
//
// Credentials: CF_API_KEY (CF API token), CF_ACCOUNT_ID (CF account ID)
package cloudflare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/getnvoi/nvoi/pkg/provider"
)

const cfBaseURL = "https://api.cloudflare.com/client/v4"

// Client manages R2 buckets via Cloudflare API + S3-compatible operations.
type Client struct {
	apiKey    string
	accountID string
	creds     *provider.BucketCredentials
}

// New creates a Cloudflare R2 bucket provider.
func New(creds map[string]string) *Client {
	return &Client{
		apiKey:    creds["api_key"],
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
	_, err := c.tokenVerify()
	if err != nil {
		return fmt.Errorf("cloudflare r2: %w", err)
	}
	return nil
}

func (c *Client) EnsureBucket(ctx context.Context, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/accounts/%s/r2/buckets", cfBaseURL, c.accountID),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 200/201 = created, 409 = already exists
	if resp.StatusCode == 200 || resp.StatusCode == 201 || resp.StatusCode == 409 {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("cloudflare r2: create bucket %s: %d: %s", name, resp.StatusCode, string(data))
}

func (c *Client) EmptyBucket(ctx context.Context, name string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3EmptyBucket(ctx, cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name)
}

func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		fmt.Sprintf("%s/accounts/%s/r2/buckets/%s", cfBaseURL, c.accountID, name), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 200 = deleted, 404 = already gone
	if resp.StatusCode == 200 || resp.StatusCode == 404 {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("cloudflare r2: delete bucket %s: %d: %s", name, resp.StatusCode, string(data))
}

func (c *Client) SetCORS(ctx context.Context, name string, origins, methods []string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3SetCORS(cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name, origins, methods)
}

func (c *Client) ClearCORS(ctx context.Context, name string) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3ClearCORS(cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name)
}

func (c *Client) SetLifecycle(ctx context.Context, name string, expireDays int) error {
	cr, err := c.Credentials(ctx)
	if err != nil {
		return err
	}
	return s3SetLifecycle(cr.Endpoint, cr.AccessKeyID, cr.SecretAccessKey, cr.Region, name, expireDays)
}

// Credentials returns S3-compatible access details derived from the CF API token.
// R2 uses the token ID as access key and SHA-256(token) as secret key.
func (c *Client) Credentials(ctx context.Context) (provider.BucketCredentials, error) {
	if c.creds != nil {
		return *c.creds, nil
	}
	tokenID, err := c.tokenVerify()
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

func (c *Client) tokenVerify() (string, error) {
	req, err := http.NewRequest("GET", cfBaseURL+"/user/tokens/verify", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Result.ID == "" {
		return "", fmt.Errorf("invalid API token")
	}
	return result.Result.ID, nil
}

var _ provider.BucketProvider = (*Client)(nil)
