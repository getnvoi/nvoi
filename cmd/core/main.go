package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/getnvoi/nvoi/internal/core"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := core.Root().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
