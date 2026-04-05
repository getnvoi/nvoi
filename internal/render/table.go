package render

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// ── Styles ──────────────────────────────────────────────────────────────────────

const margin = 2

var (
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	cellStyle   = lipgloss.NewStyle().Padding(0, 1)
	titleStyle  = lipgloss.NewStyle().Bold(true).MarginLeft(margin)
	DimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// ── Table ───────────────────────────────────────────────────────────────────────

// Table collects headers + rows. Render via Print() or through a TableGroup.
type Table struct {
	headers []string
	rows    [][]string
}

func NewTable(headers ...string) *Table {
	return &Table{headers: headers}
}

func (t *Table) Row(cols ...string) {
	t.rows = append(t.rows, cols)
}

// render builds a lipgloss table and returns the rendered string.
// width=0 means natural width.
func (t *Table) render(width int) string {
	lt := table.New().
		Headers(t.headers...).
		Border(lipgloss.RoundedBorder()).
		BorderRow(true).
		BorderStyle(borderStyle).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return cellStyle
		})

	for _, r := range t.rows {
		lt.Row(r...)
	}

	if width > 0 {
		lt.Width(width)
	}

	return lt.Render()
}

// Print renders a single table at natural width with left margin.
func (t *Table) Print() {
	fmt.Println(indent(t.render(0), margin))
}

// ── TableGroup ──────────────────────────────────────────────────────────────────

// TableGroup collects titled tables and renders them all at the same width.
type TableGroup struct {
	entries []groupEntry
}

type groupEntry struct {
	title string
	table *Table
}

func NewTableGroup() *TableGroup {
	return &TableGroup{}
}

// Add creates a new table with the given title and headers, adds it to the group,
// and returns the table so the caller can add rows.
func (g *TableGroup) Add(title string, headers ...string) *Table {
	t := NewTable(headers...)
	g.entries = append(g.entries, groupEntry{title: title, table: t})
	return t
}

// Print renders all tables at the width of the widest one.
func (g *TableGroup) Print() {
	if len(g.entries) == 0 {
		return
	}

	// First pass: render at natural width, find the widest.
	natural := make([]string, len(g.entries))
	maxWidth := 0
	for i, e := range g.entries {
		natural[i] = e.table.render(0)
		if w := measureWidth(natural[i]); w > maxWidth {
			maxWidth = w
		}
	}

	// Second pass: render all at maxWidth with left margin.
	fmt.Println()
	for i, e := range g.entries {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(titleStyle.Render(e.title))
		w := measureWidth(natural[i])
		if w < maxWidth {
			fmt.Println(indent(e.table.render(maxWidth), 0))
		} else {
			fmt.Println(indent(natural[i], 0))
		}
	}
	fmt.Println()
}

// ── Helpers ─────────────────────────────────────────────────────────────────────

// measureWidth returns the visible width of the widest line in a rendered string.
func measureWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if w := lipgloss.Width(line); w > max {
			max = w
		}
	}
	return max
}

// indent prepends n spaces to every line.
func indent(s string, n int) string {
	if n <= 0 {
		n = margin
	}
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}
