package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh [command...]",
		Short: "Run a command on the host server",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 1:
			// 1. Resolve master from provider API → SSH
			// 2. ExecOrFail(shellJoin(args)) on remote
			return fmt.Errorf("not implemented")
		},
	}
}
