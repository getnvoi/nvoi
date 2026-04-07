// Package s3 provides S3-compatible operations with AWS Signature V4 signing.
// Pure — no config, no env vars, no side effects.
package s3

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Put uploads data to an S3-compatible endpoint.
func Put(endpoint, accessKey, secretKey, bucket, key string, body []byte) error {
	url := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	Sign(req, body, accessKey, secretKey, "auto")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 put %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// Get downloads an object from an S3-compatible endpoint.
// Returns the body bytes and content type. Returns error for non-2xx responses.
func Get(endpoint, accessKey, secretKey, bucket, key string) ([]byte, string, error) {
	url := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Content-Type", "application/octet-stream")
	Sign(req, nil, accessKey, secretKey, "auto")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("s3 get %d: %s/%s", resp.StatusCode, bucket, key)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// Sign adds AWS Signature V4 headers to an HTTP request.
// region is the S3 region ("auto" for R2, "us-east-1" for AWS, etc.)
func Sign(req *http.Request, body []byte, accessKey, secretKey, region string) {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	payloadHash := sha256Hex(body)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Host", req.URL.Host)

	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.Header.Get("Content-Type"), req.URL.Host, payloadHash, amzDate)
	canonicalRequest := strings.Join([]string{
		req.Method, canonicalURI, req.URL.Query().Encode(), canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", datestamp, region)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, sha256Hex([]byte(canonicalRequest)))

	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature))
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
