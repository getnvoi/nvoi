package main

import "github.com/spf13/cobra"

func newSSHCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- <command>",
		Short: "Run command on master node",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return m.backend.SSH(cmd.Context(), args)
		},
	}
}
