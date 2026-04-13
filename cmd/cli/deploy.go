package main

import "github.com/spf13/cobra"

func newDeployCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			return m.backend.Deploy(cmd.Context())
		},
	}
}
