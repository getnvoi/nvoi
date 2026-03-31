package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec [service] -- [command...]",
		Short: "Run a command in a service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			command := args[1:]

			_ = service
			_ = command

			// TODO Phase 2:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl exec deployment/nvoi-{ws}-{service} -- {command} (on remote)
			return fmt.Errorf("not implemented")
		},
	}
}
