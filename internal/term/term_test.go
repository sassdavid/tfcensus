package term_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/term"
)

func TestUseColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode term.ColorMode
		env  map[string]string
		tty  bool
		want bool
	}{
		{"auto tty", term.ColorAuto, nil, true, true},
		{"auto pipe", term.ColorAuto, nil, false, false},
		{"NO_COLOR", term.ColorAuto, map[string]string{"NO_COLOR": "1"}, true, false},
		{"NO_COLOR any value", term.ColorAuto, map[string]string{"NO_COLOR": "yes"}, true, false},
		{"empty NO_COLOR ignored", term.ColorAuto, map[string]string{"NO_COLOR": ""}, true, true},
		{"CLICOLOR_FORCE", term.ColorAuto, map[string]string{"CLICOLOR_FORCE": "1"}, false, true},
		{"CLICOLOR_FORCE=0", term.ColorAuto, map[string]string{"CLICOLOR_FORCE": "0"}, false, false},
		{"FORCE_COLOR", term.ColorAuto, map[string]string{"FORCE_COLOR": "3"}, false, true},
		{"FORCE_COLOR=false", term.ColorAuto, map[string]string{"FORCE_COLOR": "false"}, false, false},
		{
			"NO_COLOR beats force",
			term.ColorAuto,
			map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"},
			false,
			false,
		},
		{"CLICOLOR=0", term.ColorAuto, map[string]string{"CLICOLOR": "0"}, true, false},
		{"TERM=dumb", term.ColorAuto, map[string]string{"TERM": "dumb"}, true, false},
		{"force beats TERM=dumb", term.ColorAuto, map[string]string{"TERM": "dumb", "FORCE_COLOR": "1"}, true, true},
		{"always beats NO_COLOR", term.ColorAlways, map[string]string{"NO_COLOR": "1"}, false, true},
		{"never beats tty", term.ColorNever, nil, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok }
			assert.Equal(t, tt.want, term.UseColor(tt.mode, lookup, tt.tty))
		})
	}
	_, err := term.ParseColorMode("sometimes")
	require.Error(t, err)
}

func TestDetectBuffer(t *testing.T) {
	t.Parallel()
	none := func(string) (string, bool) { return "", false }
	assert.Equal(t, term.Stream{}, term.Detect(&bytes.Buffer{}, term.ColorAuto, none))
	assert.Equal(t, term.Stream{Color: true}, term.Detect(&bytes.Buffer{}, term.ColorAlways, none),
		"no width off a terminal, even with color forced")
}

func TestWriterProfiles(t *testing.T) {
	t.Parallel()
	styled := term.Bad.Paint("error") + " " + term.Heading.Paint("title")
	var on, off bytes.Buffer
	_, err := term.Writer(&on, term.Stream{Color: true}.Profile()).Write([]byte(styled))
	require.NoError(t, err)
	_, err = term.Writer(&off, term.Stream{}.Profile()).Write([]byte(styled))
	require.NoError(t, err)
	assert.Equal(t, "\x1b[31merror\x1b[m \x1b[1mtitle\x1b[m", on.String(), "basic ANSI colors only")
	assert.Equal(t, "error title", off.String())
	assert.Equal(t, colorprofile.NoTTY, term.Stream{TTY: true}.Profile())
}

func TestSanitize(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"plain text, ≈ and 日本語":       "plain text, ≈ and 日本語",
		"tab\tand\nnewline":           "tab\tand\nnewline",
		"crlf\r\nline":                "crlf\nline",
		"bare\rreturn":                "bare^Mreturn",
		"\x1b]52;c;aGk=\x07clipboard": "^[]52;c;aGk=^Gclipboard",
		"\x1b[2Jclear":                "^[[2Jclear",
		"del\x7f":                     "del^?",
		"c1\u009b31m":                 "c1^[[31m",
	}
	for in, want := range tests {
		assert.Equal(t, want, term.Sanitize(in), "%q", in)
	}
}

func TestTable(t *testing.T) {
	t.Parallel()
	got := term.Table([]string{"TOOL", "CURRENT", "PIN"}, [][]term.Cell{
		{{Text: "go"}, {Text: "≈1.27.0", Tone: term.Warn}, {Text: "go.mod:3"}},
		{{Text: "日本語"}, {Text: "1.0.0"}, {Text: "x"}},
		{{Text: "evil\x1b[2J"}, {Text: "2"}, {Text: "y"}},
	})
	var plain bytes.Buffer
	_, err := term.Writer(&plain, colorprofile.NoTTY).Write([]byte(got))
	require.NoError(t, err)
	want := "" +
		"TOOL       CURRENT  PIN\n" +
		"go         ≈1.27.0  go.mod:3\n" +
		"日本語     1.0.0    x\n" +
		"evil^[[2J  2        y\n"
	assert.Equal(t, want, plain.String(), "display width, no trailing spaces, control characters shown")
	assert.Contains(t, got, "\x1b[33m≈1.27.0\x1b[m  ", "the gap stays outside the style")
	assert.Empty(t, term.Table(nil, nil))
	assert.Equal(t, "a  b\n", term.Table(nil, term.Rows([][]string{{"a", "b"}})))
}

func TestWrap(t *testing.T) {
	t.Parallel()
	text := "one two three four five six seven https://example.org/a/very/long/path/that/stays/whole"
	got := term.Wrap(text, 24, "  - ", "    ")
	for l := range strings.SplitSeq(got, "\n") {
		if !strings.Contains(l, "https://") {
			assert.LessOrEqual(t, len(l), 24, "%q", l)
		}
	}
	assert.True(t, strings.HasPrefix(got, "  - one two"))
	assert.Contains(t, got, "\n    https://example.org/a/very/long/path/that/stays/whole", "a long word isn't cut")
	assert.Equal(t, "  - "+text, term.Wrap(text, 0, "  - ", "    "), "0 means no wrapping")
	assert.Equal(t, "  - "+text, term.Wrap(text, 12, "  - ", "    "), "too narrow to wrap")
}

func TestIsBrokenPipe(t *testing.T) {
	t.Parallel()
	assert.True(
		t,
		term.IsBrokenPipe(
			fmt.Errorf("write output: %w", &os.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}),
		),
	)
	assert.False(t, term.IsBrokenPipe(errors.New("disk full")))
	assert.False(t, term.IsBrokenPipe(nil))
}

func TestNoteTone(t *testing.T) {
	t.Parallel()
	assert.Equal(t, term.Warn, term.NoteTone("warning: no token"))
	assert.Equal(t, term.Bad, term.NoteTone("error: denied"))
	assert.Equal(t, term.Bad, term.NoteTone("prod/eu-west-1: error: AccessDenied"))
	assert.Equal(t, term.Plain, term.NoteTone("kubectl 1.30 (mise.toml:3): latest from tags"))
}
