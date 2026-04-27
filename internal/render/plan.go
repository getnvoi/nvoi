package render

import (
	"fmt"
	"sort"

	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// RenderPlan prints a Plan as plain text. Output format:
//
//	Plan: 4 to add, 1 to change, 2 to delete
//
//	  + server worker-2  (cax21 nbg1)
//	  + firewall nvoi-myapp-prod-worker-fw
//	  ~ firewall-rule nvoi-myapp-prod-master-fw:80  was [0.0.0.0/0]
//	    ⚠ removes existing access
//	  - dns api.example.com
//	  ~ workload api  (image rebuilt, image-tag, auto)
//
//	1 change auto-applies; 5 will require confirmation on `nvoi deploy`
//
// Auto-skip entries (Reason set) are tagged so the operator sees both
// what'll happen AND which entries will skip the prompt.
func RenderPlan(plan *reconcile.Plan) {
	if plan == nil || plan.IsEmpty() {
		fmt.Println("No changes.")
		return
	}

	add, del, upd := 0, 0, 0
	for _, e := range plan.Entries {
		switch e.Kind {
		case provider.PlanAdd:
			add++
		case provider.PlanDelete:
			del++
		case provider.PlanUpdate:
			upd++
		}
	}
	fmt.Printf("Plan: %s, %s, %s\n\n",
		pluralizeChange(add, "to add"),
		pluralizeChange(upd, "to change"),
		pluralizeChange(del, "to delete"),
	)

	// Group entries by Resource so the output reads top-to-bottom in a
	// human-friendly order. Stable sort within each group by Name.
	grouped := groupByResource(plan.Entries)
	for _, res := range resourceOrder() {
		entries, ok := grouped[res]
		if !ok {
			continue
		}
		for _, e := range entries {
			line := fmt.Sprintf("  %s %s %s", e.Kind.Glyph(), e.Resource, e.Name)
			if e.Detail != "" {
				line += "  " + e.Detail
			}
			if e.Reason != "" {
				line += fmt.Sprintf("  (%s, auto)", e.Reason)
			}
			fmt.Println(line)
		}
	}

	prompted := len(plan.Promptable())
	auto := len(plan.Entries) - prompted
	fmt.Println()
	switch {
	case prompted == 0:
		fmt.Println("All changes will auto-apply on `nvoi deploy`.")
	case auto == 0:
		fmt.Printf("All %d changes will require confirmation on `nvoi deploy`.\n", prompted)
	default:
		fmt.Printf("%d auto-applies; %d will require confirmation on `nvoi deploy`.\n", auto, prompted)
	}
}

func pluralizeChange(n int, suffix string) string {
	return fmt.Sprintf("%d %s", n, suffix)
}

func groupByResource(entries []provider.PlanEntry) map[string][]provider.PlanEntry {
	out := map[string][]provider.PlanEntry{}
	for _, e := range entries {
		out[e.Resource] = append(out[e.Resource], e)
	}
	for k := range out {
		group := out[k]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Kind != group[j].Kind {
				return kindRank(group[i].Kind) < kindRank(group[j].Kind)
			}
			return group[i].Name < group[j].Name
		})
		out[k] = group
	}
	return out
}

// resourceOrder returns the rendering order for resource categories —
// matches reconcile.Deploy's pipeline so the plan reads top-to-bottom
// in apply order.
func resourceOrder() []string {
	return []string{
		provider.ResServer,
		provider.ResFirewall,
		provider.ResFirewallRule,
		provider.ResVolume,
		provider.ResNetwork,
		provider.ResBucket,
		provider.ResDatabase,
		provider.ResRegistrySecret,
		provider.ResWorkload,
		provider.ResCronJob,
		provider.ResSecretKey,
		provider.ResDNS,
		provider.ResCaddyRoute,
		provider.ResTunnel,
		provider.ResNamespace,
	}
}

// kindRank: adds before changes before deletes (matches the typical
// terraform plan reading order).
func kindRank(k provider.PlanKind) int {
	switch k {
	case provider.PlanAdd:
		return 0
	case provider.PlanUpdate:
		return 1
	case provider.PlanDelete:
		return 2
	}
	return 3
}
