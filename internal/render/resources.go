package render

import (
	"github.com/getnvoi/nvoi/pkg/provider"
)

// RenderResources prints resource groups as a table group.
//
// When any group carries Owned info (rg.Owned populated), the renderer
// adds an "Owned" column to that group. Owned values render as
// "yes"/"no" — readable in CI/JSON, easy to scan in TUI. Groups
// without Owned info render as before.
//
// Empty groups render with a single placeholder row reading "(none)"
// across all columns rather than a header-only table — confirms the
// section was checked and is genuinely empty rather than silently
// dropped.
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
		hasOwned := len(rg.Owned) == len(rg.Rows) && len(rg.Owned) > 0
		if hasOwned {
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
			if hasOwned {
				row = append(append([]string{}, row...), ownedLabel(rg.Owned[i]))
			}
			t.Row(row...)
		}
	}
	return g
}

// ownedLabel maps the Owned bool to the table cell value. Plain text
// so the same string lands in TUI, CI, and JSON renderings.
func ownedLabel(owned bool) string {
	if owned {
		return "yes"
	}
	return "no"
}
