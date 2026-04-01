package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Table renders aligned columns to stdout.
type Table struct {
	headers []string
	rows    [][]string
	w       io.Writer
}

func NewTable(headers ...string) *Table {
	return &Table{headers: headers, w: os.Stdout}
}

func (t *Table) Row(cols ...string) {
	t.rows = append(t.rows, cols)
}

func (t *Table) Print() {
	if len(t.headers) == 0 {
		return
	}

	// Compute column widths
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = len(h)
	}
	for _, row := range t.rows {
		for i, col := range row {
			if i < len(widths) && len(col) > widths[i] {
				widths[i] = len(col)
			}
		}
	}

	// Print header
	printRow(t.w, t.headers, widths)
	// Print separator
	seps := make([]string, len(widths))
	for i, w := range widths {
		seps[i] = strings.Repeat("─", w)
	}
	printRow(t.w, seps, widths)
	// Print rows
	for _, row := range t.rows {
		printRow(t.w, row, widths)
	}
}

func printRow(w io.Writer, cols []string, widths []int) {
	for i, col := range cols {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		if i < len(widths) {
			fmt.Fprintf(w, "%-*s", widths[i], col)
		} else {
			fmt.Fprint(w, col)
		}
	}
	fmt.Fprintln(w)
}
