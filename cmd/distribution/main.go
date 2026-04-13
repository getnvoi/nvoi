// Distribution server for nvoi CLI binaries.
// Serves install.sh and proxies binaries from a private R2 bucket.
//
// Reads STORAGE_RELEASES_* env vars injected by nvoi deploy.
//
// Routes:
//
//	GET /              → install.sh
//	GET /install.sh    → embedded installer script
//	GET /latest        → current version tag (from R2 "latest" key)
//	GET /{tag}/{binary} → proxy binary from R2
//	GET /health        → health check
package main

import (
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

//go:embed install.sh
var installScript []byte

func main() {
	if len(os.Args) > 1 && os.Args[1] == "pull" {
		pull(os.Args[2:])
		return
	}

	cfg := loadConfig()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			serveObject(w, r, cfg, strings.TrimPrefix(r.URL.Path, "/"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(installScript)
	})

	mux.HandleFunc("GET /install.sh", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(installScript)
	})

	mux.HandleFunc("GET /latest", func(w http.ResponseWriter, r *http.Request) {
		data, _, err := s3.Get(cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Bucket, "latest")
		if err != nil {
			http.Error(w, "no releases yet", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "max-age=60")
		w.Write(data)
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	port := getenv("PORT", "3001")
	slog.Info("distribution server starting", "port", port, "bucket", cfg.Bucket)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

type r2Config struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

func loadConfig() r2Config {
	cfg := r2Config{
		Endpoint:        os.Getenv("STORAGE_RELEASES_ENDPOINT"),
		AccessKeyID:     os.Getenv("STORAGE_RELEASES_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("STORAGE_RELEASES_SECRET_ACCESS_KEY"),
		Bucket:          os.Getenv("STORAGE_RELEASES_BUCKET"),
	}
	if cfg.Endpoint == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" || cfg.Bucket == "" {
		slog.Error("missing STORAGE_RELEASES_* env vars — is this deployed via nvoi with a 'releases' storage bucket?")
		os.Exit(1)
	}
	return cfg
}

func serveObject(w http.ResponseWriter, r *http.Request, cfg r2Config, key string) {
	body, _, contentLength, err := s3.GetStream(cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Bucket, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	if contentLength > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLength))
	}
	if strings.HasSuffix(key, ".exe") {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", key[strings.LastIndex(key, "/")+1:]))
	}
	io.Copy(w, body)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
