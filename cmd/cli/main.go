package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/getnvoi/nvoi/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := cli.Root().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
