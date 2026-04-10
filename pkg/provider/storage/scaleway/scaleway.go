// Package scaleway implements provider.BucketProvider for Scaleway Object Storage.
// Uses S3-compatible API at https://s3.{region}.scw.cloud.
package scaleway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/s3ops"
	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type Client struct {
	accessKey string
	secretKey string
	region    string
	endpoint  string
}

func New(creds map[string]string) *Client {
	region := creds["region"]
	return &Client{
		accessKey: creds["access_key"],
		secretKey: creds["secret_key"],
		region:    region,
		endpoint:  fmt.Sprintf("https://s3.%s.scw.cloud", region),
	}
}

func (c *Client) ValidateCredentials(ctx context.Context) error {
	if c.accessKey == "" || c.secretKey == "" || c.region == "" {
		return fmt.Errorf("scaleway storage: access_key, secret_key, and region are required")
	}
	// Verify by listing buckets (HEAD request to endpoint)
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/?max-keys=1", nil)
	if err != nil {
		return err
	}
	s3.Sign(req, nil, c.accessKey, c.secretKey, c.region)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("scaleway storage: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("scaleway storage: credential check returned %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) EnsureBucket(ctx context.Context, name string) error {
	body := fmt.Sprintf(`<CreateBucketConfiguration><LocationConstraint>%s</LocationConstraint></CreateBucketConfiguration>`, c.region)
	req, err := http.NewRequestWithContext(ctx, "PUT", c.endpoint+"/"+name, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	s3.Sign(req, []byte(body), c.accessKey, c.secretKey, c.region)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("create bucket %s: %w", name, err)
	}
	resp.Body.Close()
	// 200 = created, 409 = already exists — both success
	if resp.StatusCode != 200 && resp.StatusCode != 409 {
		return fmt.Errorf("create bucket %s: status %d", name, resp.StatusCode)
	}
	return nil
}

func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.endpoint+"/"+name, nil)
	if err != nil {
		return err
	}
	s3.Sign(req, nil, c.accessKey, c.secretKey, c.region)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete bucket %s: %w", name, err)
	}
	resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil // already gone
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete bucket %s: status %d", name, resp.StatusCode)
	}
	return nil
}

func (c *Client) EmptyBucket(ctx context.Context, name string) error {
	return s3ops.EmptyBucket(ctx, c.creds(), name)
}

func (c *Client) SetCORS(ctx context.Context, name string, origins, methods []string) error {
	return s3ops.SetCORS(ctx, c.creds(), name, origins, methods)
}

func (c *Client) ClearCORS(ctx context.Context, name string) error {
	return s3ops.ClearCORS(ctx, c.creds(), name)
}

func (c *Client) SetLifecycle(ctx context.Context, name string, expireDays int) error {
	return s3ops.SetLifecycle(ctx, c.creds(), name, expireDays)
}

func (c *Client) Credentials(ctx context.Context) (provider.BucketCredentials, error) {
	return c.creds(), nil
}

func (c *Client) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	objects, err := s3.ListObjects(c.endpoint, c.accessKey, c.secretKey, "", "")
	if err != nil {
		return nil, err
	}
	rows := [][]string{}
	for _, obj := range objects {
		rows = append(rows, []string{obj.Key})
	}
	return []provider.ResourceGroup{
		{Name: "Scaleway Buckets", Columns: []string{"Name"}, Rows: rows},
	}, nil
}

func (c *Client) creds() provider.BucketCredentials {
	return provider.BucketCredentials{
		Endpoint:        c.endpoint,
		AccessKeyID:     c.accessKey,
		SecretAccessKey: c.secretKey,
		Region:          c.region,
	}
}
