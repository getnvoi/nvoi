package main

import (
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/cmd"
)

func main() {
	if err := cmd.Root().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
