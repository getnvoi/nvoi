package render

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/provider"
)

// TestBuildResourceTables_OwnedColumnAdded locks the contract that any
// group carrying Owned info gets an extra "Owned" column appended,
// rendering "yes"/"no" per row.
func TestBuildResourceTables_OwnedColumnAdded(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Servers",
			Columns: []string{"ID", "Name"},
			Rows: [][]string{
				{"1", "ours"},
				{"2", "external"},
			},
			Owned: []bool{true, false},
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
	if len(tab.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(tab.rows))
	}
	if last := tab.rows[0][len(tab.rows[0])-1]; last != "yes" {
		t.Errorf("row[0] Owned cell = %q, want yes", last)
	}
	if last := tab.rows[1][len(tab.rows[1])-1]; last != "no" {
		t.Errorf("row[1] Owned cell = %q, want no", last)
	}
}

// TestBuildResourceTables_NoOwnedColumnWhenAbsent locks the inverse:
// groups without Owned info render with their original columns —
// providers that haven't migrated yet don't get a misleading column.
func TestBuildResourceTables_NoOwnedColumnWhenAbsent(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Volumes",
			Columns: []string{"ID", "Name"},
			Rows:    [][]string{{"v1", "data"}},
			// Owned not populated.
		},
	}
	tg := BuildResourceTables(groups)
	tab := tg.entries[0].table
	if !equalStrings(tab.headers, []string{"ID", "Name"}) {
		t.Errorf("headers = %v, want [ID Name] (no Owned column)", tab.headers)
	}
	if len(tab.rows[0]) != 2 {
		t.Errorf("row width = %d, want 2", len(tab.rows[0]))
	}
}

// TestBuildResourceTables_EmptyGroupPlaceholder locks the empty-table
// behavior: a group with zero rows renders a single placeholder row
// reading "(none)" across the column count, not a header-only table.
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
		t.Errorf("placeholder row width = %d, want 3 (matches column count)", len(row))
	}
	if row[0] != "(none)" {
		t.Errorf("placeholder first cell = %q, want (none)", row[0])
	}
	for i := 1; i < len(row); i++ {
		if row[i] != "" {
			t.Errorf("placeholder cell[%d] = %q, want empty", i, row[i])
		}
	}
}

// TestBuildResourceTables_EmptyGroupWithOwnedDoesNotAddColumn locks an
// edge case: empty Rows + nil Owned must NOT add the Owned column
// (length check guards), so the placeholder row width stays equal to
// the original Columns count.
func TestBuildResourceTables_EmptyGroupWithOwnedDoesNotAddColumn(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Volumes",
			Columns: []string{"ID", "Name"},
			Rows:    nil,
			Owned:   nil,
		},
	}
	tg := BuildResourceTables(groups)
	tab := tg.entries[0].table
	if !equalStrings(tab.headers, []string{"ID", "Name"}) {
		t.Errorf("headers = %v, want [ID Name]", tab.headers)
	}
}

// TestBuildResourceTables_MixedGroups locks behavior across multiple
// groups in one render — each group decides independently whether to
// show the Owned column.
func TestBuildResourceTables_MixedGroups(t *testing.T) {
	groups := []provider.ResourceGroup{
		{
			Name:    "Servers",
			Columns: []string{"ID"},
			Rows:    [][]string{{"1"}},
			Owned:   []bool{true},
		},
		{
			Name:    "Buckets",
			Columns: []string{"Name"},
			Rows:    [][]string{{"b1"}},
			// No Owned info on this group.
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
