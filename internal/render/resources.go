package render

import (
	"github.com/getnvoi/nvoi/pkg/provider"
)

// RenderResources prints resource groups as a table group.
//
// When a group carries Scope info (rg.Scope populated), the renderer
// adds a "Scope" column whose cell value is `owned` or `external`.
// Groups without Scope info render as before.
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
		hasScope := len(rg.Scope) == len(rg.Rows) && len(rg.Scope) > 0
		if hasScope {
			columns = append(append([]string{}, rg.Columns...), "Scope")
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
			if hasScope {
				row = append(append([]string{}, row...), string(rg.Scope[i]))
			}
			t.Row(row...)
		}
	}
	return g
}
