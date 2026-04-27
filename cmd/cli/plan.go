package main

import (
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	"github.com/spf13/cobra"
)

// newPlanCmd wires `nvoi plan`. Computes the cfg-vs-live diff against
// the current cluster (read-only — no provisioning, no kube writes)
// and prints what `nvoi deploy` would change.
//
// ComputePlan owns the kube/infra client lifecycle internally —
// connects via Cluster.Kube on demand, releases at end. CLI just
// passes the loaded cfg + dc.
func newPlanCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Show what `nvoi deploy` would change without applying",
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := reconcile.ComputePlan(cmd.Context(), rt.dc, rt.cfg)
			if err != nil {
				return err
			}
			render.RenderPlan(plan)
			return nil
		},
	}
}
