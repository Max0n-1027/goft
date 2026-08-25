package console

import (
	"strings"

	"golang.org/x/text/width"
)

// padRight pads s on the right to at least cols terminal columns.
//
// fmt's own "%-40s" counts bytes, which puts a line holding a Japanese file
// name out by two columns for every character in it, so the columns are laid
// out here instead. Nothing is truncated: a name too long for its column has
// always pushed the rest of the line along, and losing part of the name would
// be the worse trade.
func padRight(s string, cols int) string {
	if n := displayWidth(s); n < cols {
		return s + strings.Repeat(" ", cols-n)
	}
	return s
}

// displayWidth is how many columns a terminal gives s.
//
// East Asian wide and fullwidth characters take two, everything else one.
// Ambiguous characters are counted as one, which is what a terminal not
// configured for an East Asian font does with them.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			n += 2
		default:
			n++
		}
	}
	return n
}
