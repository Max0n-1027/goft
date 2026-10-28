package console

import (
	"io"
	"strings"
)

// Table lays rows of cells out in columns, the way text/tabwriter does, but
// by the width a terminal gives each cell: tabwriter counts a Japanese
// character as one column where a terminal draws two, so a table with
// Japanese in it came out ragged.
//
// A cell with nothing after it on its row is not padded, and does not widen
// its column either: a long warning at the end of one row would otherwise
// push every other row's last column out to meet it.
type Table struct {
	rows [][]string
}

// Row adds a row. Empty cells at its end are dropped.
func (t *Table) Row(cells ...string) {
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	t.rows = append(t.rows, cells)
}

// WriteTo writes the rows out, two spaces between columns, and forgets them,
// so the table can be used again for the rows that follow.
func (t *Table) WriteTo(w io.Writer) (int64, error) {
	var widths []int
	for _, row := range t.rows {
		for i, cell := range row[:max(len(row)-1, 0)] {
			for len(widths) <= i {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], displayWidth(cell))
		}
	}

	var b strings.Builder
	for _, row := range t.rows {
		for i, cell := range row {
			if i == len(row)-1 {
				b.WriteString(cell)
				break
			}
			b.WriteString(padRight(cell, widths[i]+2))
		}
		b.WriteString("\n")
	}
	t.rows = nil
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}
