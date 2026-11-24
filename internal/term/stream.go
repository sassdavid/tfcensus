package term

import (
	"io"
	"os"
	"strconv"

	"github.com/charmbracelet/colorprofile"
	xterm "github.com/charmbracelet/x/term"
	"github.com/mattn/go-isatty"
)

// Stream says how to write to one output stream. [Detect] fills it once at startup.
type Stream struct {
	TTY   bool // a terminal, a Cygwin or MSYS pty included
	Color bool // ANSI styles are on
	Width int  // columns to wrap prose at; 0 means don't wrap
}

// Detect works out the [Stream] for w. lookup reads the environment.
func Detect(w io.Writer, mode ColorMode, lookup func(string) (string, bool)) Stream {
	s := Stream{TTY: IsTerminal(w)}
	// A console that refuses VT processing would print the codes as text.
	s.Color = UseColor(mode, lookup, s.TTY) && enableVirtualTerminal(w)
	if s.TTY {
		s.Width = width(w, lookup)
	}
	return s
}

// Profile is the color profile that writes styled text to this stream: ANSI with color,
// otherwise none, which strips every escape sequence.
func (s Stream) Profile() colorprofile.Profile {
	if s.Color {
		return colorprofile.ANSI
	}
	return colorprofile.NoTTY
}

// Writer wraps w so styled text written to it follows profile p.
func Writer(w io.Writer, p colorprofile.Profile) io.Writer {
	return &colorprofile.Writer{Forward: w, Profile: p}
}

// IsTerminal reports whether w is a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// width reads COLUMNS, then asks the terminal.
func width(w io.Writer, lookup func(string) (string, bool)) int {
	if v, ok := lookup("COLUMNS"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	n, _, err := xterm.GetSize(f.Fd())
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
