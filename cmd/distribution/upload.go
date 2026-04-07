//go:build ignore

// Upload CLI binaries to R2. Called by GitHub Actions release workflow.
//
// Usage: go run ./cmd/distribution/upload.go <version> <dist-dir>
//
// Env: CF_API_KEY, CF_ACCOUNT_ID
//
// Uploads each binary as {version}/{filename} and writes a "latest" marker.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: upload <version> <dist-dir>\n")
		os.Exit(1)
	}
	version := os.Args[1]
	distDir := os.Args[2]

	apiKey := os.Getenv("CF_API_KEY")
	accountID := os.Getenv("CF_ACCOUNT_ID")
	bucket := os.Getenv("R2_BUCKET")
	if apiKey == "" || accountID == "" {
		slog.Error("CF_API_KEY and CF_ACCOUNT_ID are required")
		os.Exit(1)
	}
	if bucket == "" {
		bucket = "nvoi-nvoi-prod-releases"
	}

	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
	accessKey, secretKey := deriveR2Credentials(apiKey)

	entries, err := os.ReadDir(distDir)
	if err != nil {
		slog.Error("read dist dir", "error", err)
		os.Exit(1)
	}

	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(distDir, e.Name()))
		if err != nil {
			slog.Error("read file", "file", e.Name(), "error", err)
			os.Exit(1)
		}
		key := fmt.Sprintf("%s/%s", version, e.Name())
		slog.Info("uploading", "key", key, "size", len(data))
		if err := s3.Put(endpoint, accessKey, secretKey, bucket, key, data); err != nil {
			slog.Error("upload failed", "key", key, "error", err)
			os.Exit(1)
		}
	}

	// Write "latest" marker
	slog.Info("updating latest marker", "version", version)
	if err := s3.Put(endpoint, accessKey, secretKey, bucket, "latest", []byte(version)); err != nil {
		slog.Error("update latest", "error", err)
		os.Exit(1)
	}

	slog.Info("done", "version", version, "files", len(entries))
}

// deriveR2Credentials derives S3-compatible credentials from a CF API token.
// AccessKeyID = token ID from /user/tokens/verify
// SecretAccessKey = SHA256(apiKey) as hex
func deriveR2Credentials(apiKey string) (accessKey, secretKey string) {
	req, _ := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("verify CF token", "error", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Result.ID == "" {
		slog.Error("invalid CF API token")
		os.Exit(1)
	}

	hash := sha256.Sum256([]byte(apiKey))
	return result.Result.ID, hex.EncodeToString(hash[:])
}
