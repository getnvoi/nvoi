package render

import (
	"github.com/getnvoi/nvoi/pkg/provider"
)

// RenderResources prints resource groups as a table group.
//
// When a group carries Ownership info (rg.Ownership populated), the
// renderer adds an "Owned" column whose cell value is the four-state
// Ownership string verbatim (`live`, `stale`, `other`, `no`). Same
// string in TUI, CI, and JSON renderings. Groups without Ownership
// info render as before.
//
// Empty groups render with a single placeholder row reading "(none)"
// across all columns rather than a header-only table.
func RenderResources(groups []provider.ResourceGroup) {
	BuildResourceTables(groups).Print()
}

// BuildResourceTables returns the prepared TableGroup without printing.
// Pure — used by RenderResources for the side-effecting output AND by
// tests to assert the constructed headers/rows.
func BuildResourceTables(groups []provider.ResourceGroup) *TableGroup {
	g := NewTableGroup()
	for _, rg := range groups {
		columns := rg.Columns
		hasOwnership := len(rg.Ownership) == len(rg.Rows) && len(rg.Ownership) > 0
		if hasOwnership {
			columns = append(append([]string{}, rg.Columns...), "Owned")
		}

		t := g.Add(rg.Name, columns...)

		if len(rg.Rows) == 0 {
			placeholder := []string{"(none)"}
			for i := 1; i < len(columns); i++ {
				placeholder = append(placeholder, "")
			}
			t.Row(placeholder...)
			continue
		}

		for i, row := range rg.Rows {
			if hasOwnership {
				row = append(append([]string{}, row...), string(rg.Ownership[i]))
			}
			t.Row(row...)
		}
	}
	return g
}
