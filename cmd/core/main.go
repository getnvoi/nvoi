package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/getnvoi/nvoi/internal/core"
	_ "github.com/getnvoi/nvoi/pkg/provider/aws"        // register
	_ "github.com/getnvoi/nvoi/pkg/provider/cloudflare" // register
	_ "github.com/getnvoi/nvoi/pkg/provider/daytona"    // register
	_ "github.com/getnvoi/nvoi/pkg/provider/github"     // register
	_ "github.com/getnvoi/nvoi/pkg/provider/hetzner"    // register
	_ "github.com/getnvoi/nvoi/pkg/provider/local"      // register
	_ "github.com/getnvoi/nvoi/pkg/provider/scaleway"   // register
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := core.Root().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
