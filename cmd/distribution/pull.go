package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

// pull downloads release binaries from GitHub and uploads them to R2.
// Runs inside the cluster where STORAGE_RELEASES_* env vars are available.
//
// Usage: nvoi-dist pull <version> [--repo owner/repo]
func pull(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: nvoi-dist pull <version> [--repo owner/repo]\n")
		os.Exit(1)
	}
	version := args[0]

	repo := "getnvoi/nvoi"
	for i, a := range args {
		if a == "--repo" && i+1 < len(args) {
			repo = args[i+1]
		}
	}

	cfg := loadConfig()
	client := &http.Client{Timeout: 5 * time.Minute}

	// Fetch release assets from GitHub API
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, version)
	slog.Info("fetching release", "url", apiURL)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("fetch release", "error", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("release not found", "status", resp.StatusCode, "body", string(body))
		os.Exit(1)
	}

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		slog.Error("decode release", "error", err)
		os.Exit(1)
	}

	if len(release.Assets) == 0 {
		slog.Error("no assets in release", "version", version)
		os.Exit(1)
	}

	// Download each asset and upload to R2
	for _, asset := range release.Assets {
		slog.Info("downloading", "name", asset.Name, "size", asset.Size)

		resp, err := client.Get(asset.BrowserDownloadURL)
		if err != nil {
			slog.Error("download asset", "name", asset.Name, "error", err)
			os.Exit(1)
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			slog.Error("read asset", "name", asset.Name, "error", err)
			os.Exit(1)
		}

		key := fmt.Sprintf("%s/%s", version, asset.Name)
		slog.Info("uploading to R2", "key", key, "size", len(data))
		if err := s3.Put(cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Bucket, key, data); err != nil {
			slog.Error("upload failed", "key", key, "error", err)
			os.Exit(1)
		}
	}

	// Update latest marker
	slog.Info("updating latest marker", "version", version)
	if err := s3.Put(cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Bucket, "latest", []byte(version)); err != nil {
		slog.Error("update latest", "error", err)
		os.Exit(1)
	}

	slog.Info("done", "version", version, "assets", len(release.Assets))
}
