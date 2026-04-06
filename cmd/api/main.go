package main

import (
	"log/slog"
	"os"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/api/handlers"

	// Register all providers at startup — the executor resolves them by name.
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare"
	_ "github.com/getnvoi/nvoi/pkg/provider/daytona"
	_ "github.com/getnvoi/nvoi/pkg/provider/github"
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"
	_ "github.com/getnvoi/nvoi/pkg/provider/local"
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"
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
