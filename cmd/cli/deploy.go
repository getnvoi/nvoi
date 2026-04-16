package main

import (
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/spf13/cobra"
)

func newDeployCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy from config YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			return reconcile.Deploy(cmd.Context(), rt.dc, rt.cfg)
		},
	}
}
