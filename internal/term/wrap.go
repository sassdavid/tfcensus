package term

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Wrap breaks s at spaces so each line fits width display cells, starting the first line with
// first and the others with rest. A word longer than the line stays whole, so URLs keep working.
// A width of 0 or one too small for the prefixes leaves s on one line.
func Wrap(s string, width int, first, rest string) string {
	const minRoom = 10 // narrower than this, wrapping hurts more than it helps
	room := width - max(ansi.StringWidth(first), ansi.StringWidth(rest))
	if width <= 0 || room < minRoom {
		return first + s
	}
	lines := strings.Split(ansi.Wordwrap(s, room, ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return first + strings.Join(lines, "\n"+rest)
}
