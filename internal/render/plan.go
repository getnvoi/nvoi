package render

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"charm.land/lipgloss/v2"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// Plan-output styles, layered on top of the existing TUI palette so the
// `nvoi plan` and `nvoi deploy` plan-preamble visually match the rest
// of the deploy stream.
var (
	planHeader      = lipgloss.NewStyle().Bold(true).MarginLeft(2)
	planAddGlyph    = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	planUpdateGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	planDeleteGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	planEntry       = lipgloss.NewStyle().MarginLeft(4)
	planAuto        = DimStyle
)

// ── Deploy-preamble renderer (existing) ─────────────────────────────

// RenderPlan prints the CHANGES portion of a plan as a compact line
// list — used by the `nvoi deploy` prompt preamble. PlanNoChange
// entries are filtered out so the preamble only shows what's about to
// happen. For the full inventory (status of every in-scope item), the
// `nvoi plan` standalone command uses RenderPlanInventory.
func RenderPlan(plan *reconcile.Plan) {
	changes := planChanges(plan)
	if len(changes) == 0 {
		fmt.Println(planEntry.Render("✓ No changes."))
		return
	}

	add, del, upd := 0, 0, 0
	for _, e := range changes {
		switch e.Status {
		case provider.PlanAdd:
			add++
		case provider.PlanDelete:
			del++
		case provider.PlanUpdate:
			upd++
		}
	}
	header := fmt.Sprintf("Plan: %d to add, %d to change, %d to delete", add, upd, del)
	fmt.Println(planHeader.Render(header))

	grouped := groupByResource(changes)
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
	auto := len(changes) - prompted
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

// ── Standalone `nvoi plan` renderer (single inventory table) ────────

// RenderPlanInventory prints every plan entry — including
// PlanNoChange — as one inventory table, plus a summary footer. Three
// output modes:
//
//   - jsonOut: structured JSON with `summary` + `entries`. CI/scripts
//     parse this for downstream tooling.
//   - default (TUI / plain): single lipgloss table with the same
//     palette as describe / resources — STATUS, TYPE, NAME, DETAILS.
//     Sorted: changes (add / change / remove) first, then unchanged.
//
// Sort within each status bucket: by TYPE (apply order), then NAME.
func RenderPlanInventory(plan *reconcile.Plan, jsonOut bool) {
	if jsonOut {
		renderPlanJSON(plan)
		return
	}
	renderPlanTable(plan)
}

func renderPlanJSON(plan *reconcile.Plan) {
	type jsonEntry struct {
		Status  string `json:"status"`
		Layer   string `json:"layer"`
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Details string `json:"details,omitempty"`
		Reason  string `json:"reason,omitempty"`
	}
	type jsonSummary struct {
		Add       int `json:"add"`
		Change    int `json:"change"`
		Unchanged int `json:"unchanged"`
		Remove    int `json:"remove"`
	}
	type jsonPlan struct {
		Summary jsonSummary `json:"summary"`
		Entries []jsonEntry `json:"entries"`
	}

	out := jsonPlan{Entries: make([]jsonEntry, 0, len(plan.Entries))}
	for _, e := range plan.Entries {
		out.Entries = append(out.Entries, jsonEntry{
			Status:  e.Status.Word(),
			Layer:   string(e.Kind.Layer()),
			Kind:    string(e.Kind),
			Name:    e.Name,
			Details: e.Detail,
			Reason:  e.Reason,
		})
		switch e.Status {
		case provider.PlanAdd:
			out.Summary.Add++
		case provider.PlanUpdate:
			out.Summary.Change++
		case provider.PlanDelete:
			out.Summary.Remove++
		case provider.PlanNoChange:
			out.Summary.Unchanged++
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func renderPlanTable(plan *reconcile.Plan) {
	if plan == nil || len(plan.Entries) == 0 {
		fmt.Println(planEntry.Render("✓ No items in scope."))
		return
	}

	// Counts mirror the deploy preamble's add/change/delete tally with
	// "unchanged" added for the inventory case.
	add, upd, del, noc := 0, 0, 0, 0
	for _, e := range plan.Entries {
		switch e.Status {
		case provider.PlanAdd:
			add++
		case provider.PlanUpdate:
			upd++
		case provider.PlanDelete:
			del++
		case provider.PlanNoChange:
			noc++
		}
	}
	header := fmt.Sprintf("Plan: %d to add, %d to change, %d unchanged, %d to remove",
		add, upd, noc, del)
	fmt.Println(planHeader.Render(header))

	// Sort: changes first (add/change/delete in apply order), then
	// unchanged at the bottom. Within each bucket: by type (apply
	// order), then by name.
	sorted := append([]provider.PlanEntry(nil), plan.Entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri := statusRank(sorted[i].Status)
		rj := statusRank(sorted[j].Status)
		if ri != rj {
			return ri < rj
		}
		ti := typeRank(sorted[i].Kind)
		tj := typeRank(sorted[j].Kind)
		if ti != tj {
			return ti < tj
		}
		return sorted[i].Name < sorted[j].Name
	})

	t := NewTable("STATUS", "TYPE", "NAME", "DETAILS")
	for _, e := range sorted {
		t.Row(formatStatusCell(e.Status), string(e.Kind), e.Name, formatDetailCell(e))
	}
	t.Print()
}

// formatStatusCell returns "<glyph> <word>" with the glyph colored.
func formatStatusCell(s provider.PlanStatus) string {
	glyph := s.Glyph()
	switch s {
	case provider.PlanAdd:
		glyph = planAddGlyph.Render(glyph)
	case provider.PlanUpdate:
		glyph = planUpdateGlyph.Render(glyph)
	case provider.PlanDelete:
		glyph = planDeleteGlyph.Render(glyph)
	case provider.PlanNoChange:
		glyph = planAuto.Render(glyph)
	}
	return glyph + " " + s.Word()
}

// formatDetailCell joins Detail + (auto reason annotation when set).
// In the inventory table the Reason annotation is dimmed so it reads
// as supplementary info.
func formatDetailCell(e provider.PlanEntry) string {
	if e.Reason == "" {
		return e.Detail
	}
	if e.Detail == "" {
		return planAuto.Render(fmt.Sprintf("(%s)", e.Reason))
	}
	return e.Detail + " " + planAuto.Render(fmt.Sprintf("(%s)", e.Reason))
}

// formatEntry is the deploy-preamble line formatter: "<glyph> <kind> <name>  <detail>".
func formatEntry(e provider.PlanEntry) string {
	glyph := e.Status.Glyph()
	switch e.Status {
	case provider.PlanAdd:
		glyph = planAddGlyph.Render(glyph)
	case provider.PlanUpdate:
		glyph = planUpdateGlyph.Render(glyph)
	case provider.PlanDelete:
		glyph = planDeleteGlyph.Render(glyph)
	}
	line := fmt.Sprintf("%s %s %s", glyph, e.Kind, e.Name)
	if e.Detail != "" {
		line += "  " + e.Detail
	}
	if e.Reason != "" {
		line += planAuto.Render(fmt.Sprintf("  (%s, auto)", e.Reason))
	}
	return line
}

func planChanges(plan *reconcile.Plan) []provider.PlanEntry {
	if plan == nil {
		return nil
	}
	out := make([]provider.PlanEntry, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		if e.Status != provider.PlanNoChange {
			out = append(out, e)
		}
	}
	return out
}

func groupByResource(entries []provider.PlanEntry) map[provider.Kind][]provider.PlanEntry {
	out := map[provider.Kind][]provider.PlanEntry{}
	for _, e := range entries {
		out[e.Kind] = append(out[e.Kind], e)
	}
	for k := range out {
		group := out[k]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Status != group[j].Status {
				return statusRank(group[i].Status) < statusRank(group[j].Status)
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
func resourceOrder() []provider.Kind {
	return []provider.Kind{
		provider.KindServer,
		provider.KindFirewall,
		provider.KindFirewallRule,
		provider.KindVolume,
		provider.KindNetwork,
		provider.KindBucket,
		provider.KindDatabase,
		provider.KindRegistrySecret,
		provider.KindServiceWorkload,
		provider.KindCronWorkload,
		provider.KindSecretKey,
		provider.KindDNSRecord,
		provider.KindCaddyIngress,
		provider.KindTunnel,
		provider.KindTunnelAgent,
		provider.KindNamespace,
	}
}

// typeRank returns the sort position of a Kind for the inventory
// table — earlier in apply order = lower rank = closer to the top.
func typeRank(k provider.Kind) int {
	for i, r := range resourceOrder() {
		if r == k {
			return i
		}
	}
	return 999
}

// statusRank: changes (add/update/delete) before unchanged. Within
// changes: add → change → delete (the typical IaC reading order).
func statusRank(k provider.PlanKind) int {
	switch k {
	case provider.PlanAdd:
		return 0
	case provider.PlanUpdate:
		return 1
	case provider.PlanDelete:
		return 2
	case provider.PlanNoChange:
		return 9
	}
	return 99
}
