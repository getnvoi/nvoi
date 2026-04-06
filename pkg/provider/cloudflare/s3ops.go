// Shared S3 API operations: CORS, Lifecycle, Empty.
// Delegates signing to core/s3.Sign.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

// s3Client is a shared HTTP client with timeout for S3 operations.
var s3Client = &http.Client{Timeout: 30 * time.Second}

// s3EmptyBucket lists and deletes all objects in a bucket via S3 API.
// Loops until the bucket is empty.
func s3EmptyBucket(ctx context.Context, creds provider.BucketCredentials, bucket string) error {
	for {
		// List objects
		url := fmt.Sprintf("%s/%s?list-type=2&max-keys=1000", creds.Endpoint, bucket)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/xml")
		s3.Sign(req, nil, creds.AccessKeyID, creds.SecretAccessKey, creds.Region)

		resp, err := s3Client.Do(req)
		if err != nil {
			return err
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("s3 list objects %s: read body: %w", bucket, err)
		}

		if resp.StatusCode == 404 {
			return nil // bucket gone — nothing to empty
		}
		if resp.StatusCode >= 300 {
			return fmt.Errorf("s3 list objects %s: %d: %s", bucket, resp.StatusCode, string(body))
		}

		var listResult struct {
			Contents []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
			IsTruncated bool `xml:"IsTruncated"`
		}
		if err := xml.Unmarshal(body, &listResult); err != nil {
			return fmt.Errorf("s3 parse list: %w", err)
		}

		if len(listResult.Contents) == 0 {
			return nil
		}

		// Build delete request
		var sb strings.Builder
		sb.WriteString("<Delete><Quiet>true</Quiet>")
		for _, obj := range listResult.Contents {
			fmt.Fprintf(&sb, "<Object><Key>%s</Key></Object>", obj.Key)
		}
		sb.WriteString("</Delete>")

		deleteBody := []byte(sb.String())
		deleteURL := fmt.Sprintf("%s/%s?delete", creds.Endpoint, bucket)
		deleteReq, err := http.NewRequestWithContext(ctx, "POST", deleteURL, bytes.NewReader(deleteBody))
		if err != nil {
			return err
		}
		deleteReq.Header.Set("Content-Type", "application/xml")
		s3.Sign(deleteReq, deleteBody, creds.AccessKeyID, creds.SecretAccessKey, creds.Region)

		deleteResp, err := s3Client.Do(deleteReq)
		if err != nil {
			return err
		}
		deleteResp.Body.Close()

		if deleteResp.StatusCode >= 300 {
			return fmt.Errorf("s3 delete objects %s: %d", bucket, deleteResp.StatusCode)
		}

		if !listResult.IsTruncated {
			return nil
		}
	}
}

// s3SetCORS sets CORS configuration on a bucket via the S3 PutBucketCors API.
func s3SetCORS(ctx context.Context, creds provider.BucketCredentials, bucket string, origins, methods []string) error {
	var sb strings.Builder
	sb.WriteString("<CORSConfiguration><CORSRule>")
	for _, o := range origins {
		fmt.Fprintf(&sb, "<AllowedOrigin>%s</AllowedOrigin>", o)
	}
	if len(methods) == 0 {
		methods = []string{"GET", "PUT", "POST", "DELETE"}
	}
	for _, m := range methods {
		fmt.Fprintf(&sb, "<AllowedMethod>%s</AllowedMethod>", m)
	}
	sb.WriteString("<AllowedHeader>*</AllowedHeader>")
	sb.WriteString("<ExposeHeader>ETag</ExposeHeader>")
	sb.WriteString("<MaxAgeSeconds>3600</MaxAgeSeconds>")
	sb.WriteString("</CORSRule></CORSConfiguration>")

	body := []byte(sb.String())
	url := fmt.Sprintf("%s/%s?cors", creds.Endpoint, bucket)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	s3.Sign(req, body, creds.AccessKeyID, creds.SecretAccessKey, creds.Region)

	resp, err := s3Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("s3 set cors %s: %d (body unreadable: %w)", bucket, resp.StatusCode, err)
		}
		return fmt.Errorf("s3 set cors %s: %d: %s", bucket, resp.StatusCode, string(respBody))
	}
	return nil
}

// s3ClearCORS removes CORS configuration from a bucket.
// Idempotent — succeeds even if no CORS config exists.
func s3ClearCORS(ctx context.Context, creds provider.BucketCredentials, bucket string) error {
	url := fmt.Sprintf("%s/%s?cors", creds.Endpoint, bucket)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	s3.Sign(req, nil, creds.AccessKeyID, creds.SecretAccessKey, creds.Region)

	resp, err := s3Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 204 = deleted, 404 = no cors config (both fine)
	if resp.StatusCode >= 300 && resp.StatusCode != 404 {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("s3 clear cors %s: %d (body unreadable: %w)", bucket, resp.StatusCode, err)
		}
		return fmt.Errorf("s3 clear cors %s: %d: %s", bucket, resp.StatusCode, string(respBody))
	}
	return nil
}

// s3SetLifecycle sets an expiration lifecycle rule on a bucket.
func s3SetLifecycle(ctx context.Context, creds provider.BucketCredentials, bucket string, expireDays int) error {
	body := []byte(fmt.Sprintf(`<LifecycleConfiguration>
  <Rule>
    <ID>nvoi-expire</ID>
    <Status>Enabled</Status>
    <Expiration><Days>%d</Days></Expiration>
  </Rule>
</LifecycleConfiguration>`, expireDays))

	url := fmt.Sprintf("%s/%s?lifecycle", creds.Endpoint, bucket)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	s3.Sign(req, body, creds.AccessKeyID, creds.SecretAccessKey, creds.Region)

	resp, err := s3Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("s3 set lifecycle %s: %d (body unreadable: %w)", bucket, resp.StatusCode, err)
		}
		return fmt.Errorf("s3 set lifecycle %s: %d: %s", bucket, resp.StatusCode, string(respBody))
	}
	return nil
}
