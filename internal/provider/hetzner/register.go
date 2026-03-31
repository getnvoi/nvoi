package hetzner

import (
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/provider"
)

func init() {
	provider.RegisterCompute("hetzner", func() (provider.ComputeProvider, error) {
		token := os.Getenv("HETZNER_TOKEN")
		if token == "" {
			return nil, fmt.Errorf("HETZNER_TOKEN is required")
		}
		return New(token), nil
	})
}
