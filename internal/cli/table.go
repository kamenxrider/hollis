// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// printTable renders a human-readable table: a header line followed by one
// line per row. Every column except the last is left-aligned and padded to
// the width of its widest cell — header included — so each header starts at
// the same visible column as the values beneath it. Widths are counted in
// runes, so multi-byte text occupies its visible width. The last column is
// written unpadded. Cells are expected to be terminal-ready already (see
// humanTerminalText); the helper never restyles them.
func printTable(w io.Writer, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, cell := range headers {
		widths[i] = utf8.RuneCountInString(cell)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if width := utf8.RuneCountInString(cell); width > widths[i] {
				widths[i] = width
			}
		}
	}
	var out strings.Builder
	writeRow := func(cells []string) {
		for i := range headers {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			out.WriteString(cell)
			if i < len(headers)-1 {
				if pad := widths[i] - utf8.RuneCountInString(cell); pad > 0 {
					out.WriteString(strings.Repeat(" ", pad))
				}
				out.WriteString("  ")
			}
		}
		out.WriteString("\n")
	}
	writeRow(headers)
	for _, row := range rows {
		writeRow(row)
	}
	fmt.Fprint(w, out.String())
}
