package main

import (
	"github.com/getnvoi/nvoi/internal/core"
	"github.com/spf13/cobra"
)

func newTeardownCmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Tear down all provider resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			dv, _ := cmd.Flags().GetBool("delete-volumes")
			ds, _ := cmd.Flags().GetBool("delete-storage")
			return core.Teardown(cmd.Context(), rt.dc, rt.cfg, dv, ds)
		},
	}
	cmd.Flags().Bool("delete-volumes", false, "also delete persistent volumes (preserved by default)")
	cmd.Flags().Bool("delete-storage", false, "also delete storage buckets (preserved by default)")
	return cmd
}
