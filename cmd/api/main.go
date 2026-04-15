package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/handlers"
	"github.com/getsentry/sentry-go"

	// Compute
	_ "github.com/getnvoi/nvoi/pkg/provider/compute/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/compute/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/compute/scaleway"
	// DNS
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/dns/scaleway"
	// Storage
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/storage/scaleway"
	// Build
	_ "github.com/getnvoi/nvoi/pkg/provider/build/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/github"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/local"
	// Secrets
	_ "github.com/getnvoi/nvoi/pkg/provider/secrets/awssm"
	_ "github.com/getnvoi/nvoi/pkg/provider/secrets/doppler"
	_ "github.com/getnvoi/nvoi/pkg/provider/secrets/infisical"
)

func main() {
	if dsn := os.Getenv("SENTRY_DSN"); dsn != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: dsn}); err != nil {
			slog.Warn("sentry init failed", "error", err)
		} else {
			defer sentry.Flush(2 * time.Second)
		}
	}

	db, err := api.OpenDB()
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}

	if err := api.InitEncryption(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("api server starting", "port", port)
	if err := handlers.NewRouter(db, api.VerifyGitHubToken).Run(":" + port); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
