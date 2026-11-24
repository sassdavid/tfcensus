package term

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Tone is what a piece of styled text means. Each tone uses one of the 16 basic ANSI colors, so
// it follows the user's terminal theme, and the text itself always carries the meaning as well.
type Tone uint8

// Tones.
const (
	Plain   Tone = iota
	Good         // green: current, clean, ok
	Warn         // yellow: behind, drifted, needs a look
	Bad          // red: errors, vulnerabilities, unmanaged resources
	Severe       // bold red: breaking changes, end of life, known exploited
	Muted        // faint: skipped, excluded, low severity
	Heading      // bold: section and tool headings
)

// Style returns the lipgloss style of t.
func (t Tone) Style() lipgloss.Style {
	s := lipgloss.NewStyle()
	switch t {
	case Good:
		return s.Foreground(lipgloss.Green)
	case Warn:
		return s.Foreground(lipgloss.Yellow)
	case Bad:
		return s.Foreground(lipgloss.Red)
	case Severe:
		return s.Foreground(lipgloss.Red).Bold(true)
	case Muted:
		return s.Faint(true)
	case Heading:
		return s.Bold(true)
	case Plain:
	}
	return s
}

// Paint returns s in tone t, with control characters in s made visible by [Sanitize].
func (t Tone) Paint(s string) string {
	s = Sanitize(s)
	if t == Plain || s == "" {
		return s
	}
	return t.Style().Render(s)
}

// NoteTone is the tone of a note line by its label: Warn for "warning: ...", Bad for
// "error: ..." and "<target>: error: ...", Plain otherwise.
func NoteTone(line string) Tone {
	switch {
	case strings.HasPrefix(line, "warning: "):
		return Warn
	case strings.HasPrefix(line, "error: ") || strings.Contains(line, ": error: "):
		return Bad
	}
	return Plain
}
