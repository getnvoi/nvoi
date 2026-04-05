package render

import (
	"github.com/getnvoi/nvoi/pkg/provider"
)

// RenderResources prints resource groups as a table group.
func RenderResources(groups []provider.ResourceGroup) {
	g := NewTableGroup()
	for _, rg := range groups {
		t := g.Add(rg.Name, rg.Columns...)
		for _, row := range rg.Rows {
			t.Row(row...)
		}
	}
	g.Print()
}
