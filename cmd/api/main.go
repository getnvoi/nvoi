package main

import (
	"log/slog"
	"os"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/handlers"

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
	// Build
	_ "github.com/getnvoi/nvoi/pkg/provider/build/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/github"
	_ "github.com/getnvoi/nvoi/pkg/provider/build/local"
)

func main() {
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
