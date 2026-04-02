package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/getnvoi/nvoi/internal/cmd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	root := cmd.Root()
	if err := root.ExecuteContext(ctx); err != nil {
		cmd.HandleError(ctx, root, err)
		os.Exit(1)
	}
}
