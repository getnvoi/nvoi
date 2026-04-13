package main

import "github.com/spf13/cobra"

func newDescribeCmd(m *mode) *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Live cluster state",
		RunE: func(cmd *cobra.Command, args []string) error {
			j, _ := cmd.Flags().GetBool("json")
			return m.backend.Describe(cmd.Context(), j)
		},
	}
}
