package term

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// columnGap is the number of spaces between table columns.
const columnGap = 2

// Cell is one table cell.
type Cell struct {
	Text string
	Tone Tone
}

// Table renders an optional header row and rows as left-aligned columns two spaces apart, with
// no borders. It measures display cells, so wide characters and styles keep the columns aligned,
// and it never cuts a cell. Cell text goes through [Sanitize], and lines end without spaces.
func Table(header []string, rows [][]Cell) string {
	if len(header) == 0 && len(rows) == 0 {
		return ""
	}
	cols := len(header)
	texts := make([][]string, len(rows))
	for i, r := range rows {
		cols = max(cols, len(r))
		texts[i] = make([]string, len(r))
		for j, c := range r {
			texts[i][j] = Sanitize(c.Text)
		}
	}
	gap := lipgloss.NewStyle().PaddingRight(columnGap)
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).
		Rows(texts...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := gap
			if col == cols-1 {
				s = lipgloss.NewStyle()
			}
			if row >= 0 && row < len(rows) && col < len(rows[row]) {
				s = s.Inherit(rows[row][col].Tone.Style())
			}
			return s
		})
	if len(header) > 0 {
		h := make([]string, len(header))
		for i, s := range header {
			h[i] = Sanitize(s)
		}
		t.Headers(h...)
	}
	lines := strings.Split(t.String(), "\n")
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(strings.TrimRight(l, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// Rows turns rows of plain text into rows of cells for [Table].
func Rows(rows [][]string) [][]Cell {
	out := make([][]Cell, len(rows))
	for i, r := range rows {
		out[i] = make([]Cell, len(r))
		for j, s := range r {
			out[i][j] = Cell{Text: s}
		}
	}
	return out
}
