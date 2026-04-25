package render

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestBuildResourceTables_OwnershipColumnAdded locks the contract that
// any group carrying Ownership info gets an extra "Owned" column
// appended, rendering the four-state value verbatim.
func TestBuildResourceTables_OwnershipColumnAdded(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Servers",
			Columns: []string{"ID", "Name"},
			Rows: [][]string{
				{"1", "live-row"},
				{"2", "stale-row"},
				{"3", "other-row"},
				{"4", "no-row"},
			},
			Ownership: []provider.Ownership{
				provider.OwnershipLive,
				provider.OwnershipStale,
				provider.OwnershipOther,
				provider.OwnershipNone,
			},
		},
	}

	tg := BuildResourceTables(groups)
	if len(tg.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(tg.entries))
	}
	tab := tg.entries[0].table
	wantHeaders := []string{"ID", "Name", "Owned"}
	if !equalStrings(tab.headers, wantHeaders) {
		t.Errorf("headers = %v, want %v", tab.headers, wantHeaders)
	}
	wantCells := []string{"live", "stale", "other", "no"}
	for i, want := range wantCells {
		got := tab.rows[i][len(tab.rows[i])-1]
		if got != want {
			t.Errorf("row[%d] Owned cell = %q, want %q", i, got, want)
		}
	}
}

// TestBuildResourceTables_NoOwnershipColumnWhenAbsent — providers that
// haven't migrated render with their original columns, no misleading
// Owned column.
func TestBuildResourceTables_NoOwnershipColumnWhenAbsent(t *testing.T) {
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
// independently whether to show the Owned column.
func TestBuildResourceTables_MixedGroups(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:      "Servers",
			Columns:   []string{"ID"},
			Rows:      [][]string{{"1"}},
			Ownership: []provider.Ownership{provider.OwnershipLive},
		},
		{
			Name:    "Buckets",
			Columns: []string{"Name"},
			Rows:    [][]string{{"b1"}},
		},
	}
	tg := BuildResourceTables(groups)
	if len(tg.entries) != 2 {
		t.Fatalf("entries = %d", len(tg.entries))
	}
	if !equalStrings(tg.entries[0].table.headers, []string{"ID", "Owned"}) {
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
