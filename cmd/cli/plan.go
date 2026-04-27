package main

import (
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/internal/render"
	"github.com/spf13/cobra"
)

// newPlanCmd wires `nvoi plan`. Computes the cfg-vs-live diff against
// the current cluster (read-only — no provisioning, no kube writes)
// and prints the inventory: every in-scope item with a status column
// (+ add / ~ change / ~ unchanged / - remove).
//
// Output mode follows the root --json flag: structured JSON for CI /
// scripting, lipgloss-styled table otherwise.
//
// ComputePlan owns the kube/infra client lifecycle internally —
// connects via Cluster.Kube on demand, releases at end. CLI just
// passes the loaded cfg + dc.
func newPlanCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Show every in-scope item + its status (+ add / ~ change / ~ unchanged / - remove)",
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := reconcile.ComputePlan(cmd.Context(), rt.dc, rt.cfg)
			if err != nil {
				return err
			}
			jsonFlag, _ := cmd.Root().PersistentFlags().GetBool("json")
			render.RenderPlanInventory(plan, jsonFlag)
			return nil
		},
	}
}
