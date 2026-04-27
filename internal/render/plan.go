package render

import (
	"fmt"
	"sort"

	"charm.land/lipgloss/v2"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Plan-output styles, layered on top of the existing TUI palette so the
// `nvoi plan` and `nvoi deploy` plan-preamble visually match the rest
// of the deploy stream:
//
//   - planHeader      → mirror of tuiCommand (Bold + MarginLeft 2). Same
//     weight as "registry set 1 host" group headers.
//   - planAddGlyph    → bold green for `+`, like a stylized "✓" success.
//   - planUpdateGlyph → bold orange for `~`, mirror of tuiWarning's hue.
//   - planDeleteGlyph → bold red for `-`, mirror of tuiError's hue.
//   - planEntry       → MarginLeft 4 (matches tuiSuccess body indent).
//   - planAuto        → DimStyle annotation for `(image-tag, auto)` and
//     the footer summary line.
var (
	planHeader      = lipgloss.NewStyle().Bold(true).MarginLeft(2)
	planAddGlyph    = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	planUpdateGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	planDeleteGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	planEntry       = lipgloss.NewStyle().MarginLeft(4)
	planAuto        = DimStyle
)

// RenderPlan prints a Plan styled with the same lipgloss palette the
// TUI Output uses for live deploy events. Visual rhythm:
//
//	Plan: 0 to add, 1 to change, 0 to delete       ← bold, 2-space margin
//
//	  ~ workload api  image rebuilt  (image-tag, auto)
//	    └ glyph colored          └ dim annotation
//
//	All changes will auto-apply on `nvoi deploy`.   ← dim
//
// Empty plan → "No changes." (success-style ✓ in TUI; bare in plain).
//
// Color choices mirror tui.go's palette:
//   - 42  (green)  for +  → "creation, safe"
//   - 214 (amber)  for ~  → matches tuiWarning's hue (change is potentially destructive)
//   - 196 (red)   for -  → matches tuiError's hue (deletion)
func RenderPlan(plan *reconcile.Plan) {
	if plan == nil || plan.IsEmpty() {
		fmt.Println(planEntry.Render("✓ No changes."))
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
	header := fmt.Sprintf("Plan: %d to add, %d to change, %d to delete",
		add, upd, del)
	fmt.Println(planHeader.Render(header))
	fmt.Println()

	grouped := groupByResource(plan.Entries)
	for _, res := range resourceOrder() {
		entries, ok := grouped[res]
		if !ok {
			continue
		}
		for _, e := range entries {
			fmt.Println(planEntry.Render(formatEntry(e)))
		}
	}

	prompted := len(plan.Promptable())
	auto := len(plan.Entries) - prompted
	fmt.Println()
	var footer string
	switch {
	case prompted == 0:
		footer = "All changes will auto-apply on `nvoi deploy`."
	case auto == 0:
		footer = fmt.Sprintf("All %d changes will require confirmation on `nvoi deploy`.", prompted)
	default:
		footer = fmt.Sprintf("%d auto-applies; %d will require confirmation on `nvoi deploy`.", auto, prompted)
	}
	fmt.Println(planEntry.Render(planAuto.Render(footer)))
}

// formatEntry renders one plan line: kind-colored glyph, resource,
// name, optional Detail, optional dim "(reason, auto)" annotation.
func formatEntry(e provider.PlanEntry) string {
	glyph := e.Kind.Glyph()
	switch e.Kind {
	case provider.PlanAdd:
		glyph = planAddGlyph.Render(glyph)
	case provider.PlanUpdate:
		glyph = planUpdateGlyph.Render(glyph)
	case provider.PlanDelete:
		glyph = planDeleteGlyph.Render(glyph)
	}
	line := fmt.Sprintf("%s %s %s", glyph, e.Resource, e.Name)
	if e.Detail != "" {
		line += "  " + e.Detail
	}
	if e.Reason != "" {
		line += planAuto.Render(fmt.Sprintf("  (%s, auto)", e.Reason))
	}
	return line
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
