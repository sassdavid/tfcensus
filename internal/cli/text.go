package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/term"
)

// reportOut is where a command sends its report: the sink, and the width its text formats wrap
// prose at.
type reportOut struct {
	render.Sink

	width func(render.Format) int
}

func (g *globals) reportOut(cmd *cobra.Command, d *app.Deps) reportOut {
	return reportOut{Sink: g.sink(cmd, d), width: g.textWidth}
}

// textOut wraps stdout for a command that writes its text without a [render.Sink].
func (g *globals) textOut(d *app.Deps) io.Writer {
	return styledStdout(d.Stdout, g.outFormat, g.stdout.Color)
}

// stdout wraps stdout for text written outside [render.Sink.Write], such as scan --explain.
func (o reportOut) stdout() io.Writer {
	return styledStdout(o.Stdout, o.Format, o.Color)
}

// styledStdout keeps styles written to w only in the tty format on a stdout with color.
func styledStdout(w io.Writer, f render.Format, color bool) io.Writer {
	return term.Writer(w, term.Stream{Color: color && f == render.FormatTTY}.Profile())
}

// writeText writes a finished text report in one call.
func writeText(w io.Writer, s, what string) error {
	if _, err := io.WriteString(w, s); err != nil {
		return fmt.Errorf("write %s: %w", what, err)
	}
	return nil
}

// writeLines adds each line wrapped at width, starting with first and continuing with rest, in
// the tone of its label ([term.NoteTone]). Control characters, as in AWS error messages, are
// made visible.
func writeLines(b *strings.Builder, lines []string, width int, first, rest string) {
	for _, l := range lines {
		writeToned(b, term.NoteTone(l), l, width, first, rest)
	}
}

// writeToned adds line like [writeLines] does, with every wrapped line in tone t.
func writeToned(b *strings.Builder, t term.Tone, line string, width int, first, rest string) {
	for l := range strings.Lines(term.Wrap(term.Sanitize(line), width, first, rest)) {
		b.WriteString(t.Paint(strings.TrimSuffix(l, "\n")) + "\n")
	}
}

// indent puts prefix in front of every line of s.
func indent(s, prefix string) string {
	var b strings.Builder
	for line := range strings.Lines(s) {
		b.WriteString(prefix + line)
	}
	return b.String()
}

// rowsText renders rows without a header as a [term.Table], indented by two spaces.
func rowsText(rows [][]term.Cell) string {
	return indent(term.Table(nil, rows), "  ")
}

// cellTexts returns the text of each cell, for the formats without styles.
func cellTexts(row []term.Cell) []string {
	out := make([]string, len(row))
	for i, c := range row {
		out[i] = c.Text
	}
	return out
}

// cellRows turns rows of cells into rows of text.
func cellRows(rows [][]term.Cell) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = cellTexts(r)
	}
	return out
}

// toneIf returns t when cond holds, else [term.Plain].
func toneIf(cond bool, t term.Tone) term.Tone {
	if cond {
		return t
	}
	return term.Plain
}

// countCell shows n, in tone t when n is above zero.
func countCell(n int, t term.Tone) term.Cell {
	return term.Cell{Text: strconv.Itoa(n), Tone: toneIf(n > 0, t)}
}
