package main

import "github.com/spf13/cobra"

func newExecCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <service> -- <command>",
		Short: "Run command in service pod",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return m.backend.Exec(cmd.Context(), args[0], args[1:])
		},
	}
}
