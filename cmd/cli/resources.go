package main

import "github.com/spf13/cobra"

func newResourcesCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			return m.backend.Resources(cmd.Context(), j)
		},
	}
}
