package render

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestBuildResourceTables_ScopeColumnAdded locks the Scope column —
// owned/external string verbatim, appended to the original columns.
func TestBuildResourceTables_ScopeColumnAdded(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Servers",
			Columns: []string{"ID", "Name"},
			Rows: [][]string{
				{"1", "ours"},
				{"2", "external"},
			},
			Scope: []provider.Scope{provider.ScopeOwned, provider.ScopeExternal},
		},
	}

	tg := BuildResourceTables(groups)
	tab := tg.entries[0].table
	wantHeaders := []string{"ID", "Name", "Scope"}
	if !equalStrings(tab.headers, wantHeaders) {
		t.Errorf("headers = %v, want %v", tab.headers, wantHeaders)
	}
	if got := tab.rows[0][len(tab.rows[0])-1]; got != "owned" {
		t.Errorf("row[0] Scope cell = %q, want owned", got)
	}
	if got := tab.rows[1][len(tab.rows[1])-1]; got != "external" {
		t.Errorf("row[1] Scope cell = %q, want external", got)
	}
}

// TestBuildResourceTables_NoScopeColumnWhenAbsent — providers/groups
// without Scope info render with their original columns.
func TestBuildResourceTables_NoScopeColumnWhenAbsent(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Volumes",
			Columns: []string{"ID", "Name"},
			Rows:    [][]string{{"v1", "data"}},
		},
	}
	tg := BuildResourceTables(groups)
	tab := tg.entries[0].table
	if !equalStrings(tab.headers, []string{"ID", "Name"}) {
		t.Errorf("headers = %v, want [ID Name]", tab.headers)
	}
	if len(tab.rows[0]) != 2 {
		t.Errorf("row width = %d, want 2", len(tab.rows[0]))
	}
}

// TestBuildResourceTables_EmptyGroupPlaceholder locks the empty-table
// behavior: zero rows render a single "(none)" row across all columns.
func TestBuildResourceTables_EmptyGroupPlaceholder(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Volumes",
			Columns: []string{"ID", "Name", "Size"},
			Rows:    nil,
		},
	}
	tg := BuildResourceTables(groups)
	tab := tg.entries[0].table
	if len(tab.rows) != 1 {
		t.Fatalf("rows = %d, want 1 placeholder", len(tab.rows))
	}
	row := tab.rows[0]
	if len(row) != 3 {
		t.Errorf("placeholder row width = %d, want 3", len(row))
	}
	if row[0] != "(none)" {
		t.Errorf("placeholder first cell = %q, want (none)", row[0])
	}
}

// TestBuildResourceTables_MixedGroups: each group decides
// independently whether to show the Scope column.
func TestBuildResourceTables_MixedGroups(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Servers",
			Columns: []string{"ID"},
			Rows:    [][]string{{"1"}},
			Scope:   []provider.Scope{provider.ScopeOwned},
		},
		{
			Name:    "Buckets",
			Columns: []string{"Name"},
			Rows:    [][]string{{"b1"}},
		},
	}
	tg := BuildResourceTables(groups)
	if !equalStrings(tg.entries[0].table.headers, []string{"ID", "Scope"}) {
		t.Errorf("Servers headers = %v", tg.entries[0].table.headers)
	}
	if !equalStrings(tg.entries[1].table.headers, []string{"Name"}) {
		t.Errorf("Buckets headers = %v", tg.entries[1].table.headers)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
