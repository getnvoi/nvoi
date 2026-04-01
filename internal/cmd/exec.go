package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [service] -- [command...]",
		Short: "Run a command in a service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			command := args[1:]

			_, _, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			_, err = resolveComputeProvider(cmd)
			if err != nil {
				return err
			}

			_ = service
			_ = command

			// TODO Phase 2:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl exec deployment/{service} -- {command} (on remote)
			return fmt.Errorf("not implemented")
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}
