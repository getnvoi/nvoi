package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show k8s deployment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 2:
			// 1. Resolve master server from provider API → get IP
			// 2. SSH
			// 3. kubectl get deployments,statefulsets -o wide (on remote)
			return fmt.Errorf("not implemented")
		},
	}
}

func newPSCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "Show running pods",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 2:
			// 1. Resolve master server from provider API → get IP
			// 2. SSH
			// 3. kubectl get pods -o wide (on remote)
			return fmt.Errorf("not implemented")
		},
	}
}
