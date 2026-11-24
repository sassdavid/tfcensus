package render_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/render"
)

func TestParseOutputs(t *testing.T) {
	t.Parallel()
	got, err := render.ParseOutputs([]string{"json=a.json", "sarif=out/b.sarif", "md=c=d.md"})
	require.NoError(t, err)
	assert.Equal(t, []render.Output{
		{Format: render.FormatJSON, Path: "a.json"},
		{Format: render.FormatSARIF, Path: "out/b.sarif"},
		{Format: render.FormatMD, Path: "c=d.md"},
	}, got)
	tests := []struct{ spec, want string }{
		{"json", "want format=path"},
		{"=a.json", "want format=path"},
		{"json=", "want format=path"},
		{"yaml=a.yaml", `invalid --out format "yaml"`},
		{"tty=a.txt", `invalid --out format "tty"`},
	}
	for _, tt := range tests {
		_, err = render.ParseOutputs([]string{tt.spec})
		require.ErrorContains(t, err, tt.want, tt.spec)
	}
	_, err = render.ParseOutputs([]string{"json=a.json", "md=./a.json"})
	require.ErrorContains(t, err, "already an --out file")
}

func TestSinkWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	long := "## report\n\n| A |\n|---|\n" + strings.Repeat("| a row of the report |\n", 20)
	write := func(w io.Writer, f render.Format) error {
		if f == render.FormatMD {
			_, err := io.WriteString(w, long)
			return err
		}
		_, err := fmt.Fprintf(w, "format %s\n", f)
		return err
	}
	var stdout bytes.Buffer
	s := render.Sink{
		Stdout: &stdout,
		Format: render.FormatMD,
		Outputs: []render.Output{
			{Format: render.FormatJSON, Path: filepath.Join(dir, "r.json")},
			{Format: render.FormatMD, Path: filepath.Join(dir, "r.md")},
		},
		MDMaxChars: 300,
		Command:    "tfcensus drift",
	}
	require.NoError(t, s.Write(write))
	assert.Contains(t, stdout.String(), "Run `tfcensus drift --format json` for the full report.")
	assert.LessOrEqual(t, len(stdout.String()), 300)
	b, err := os.ReadFile(filepath.Join(dir, "r.json"))
	require.NoError(t, err)
	assert.Equal(t, "format json\n", string(b))
	b, err = os.ReadFile(filepath.Join(dir, "r.md"))
	require.NoError(t, err)
	assert.Equal(t, stdout.String(), string(b))

	s.Outputs = []render.Output{{Format: render.FormatPlain, Path: filepath.Join(dir, "missing", "r.txt")}}
	require.ErrorContains(t, s.Write(write), "write --out "+filepath.Join(dir, "missing", "r.txt"))
}

// brokenPipe fails every write the way a pipe whose reader has gone does.
type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}
}

func TestSinkBrokenPipe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "r.json")
	s := render.Sink{
		Stdout:  brokenPipe{},
		Format:  render.FormatPlain,
		Outputs: []render.Output{{Format: render.FormatJSON, Path: out}},
	}
	require.NoError(t, s.Write(func(w io.Writer, f render.Format) error {
		_, err := fmt.Fprintf(w, "format %s\n", f)
		return err
	}), "a closed stdout is no error")
	b, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "format json\n", string(b), "the --out file is still written")
}

func TestSinkColor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(w io.Writer, _ render.Format) error {
		_, err := io.WriteString(w, "\x1b[31mred\x1b[m\n")
		return err
	}
	tests := []struct {
		format render.Format
		color  bool
		want   string
	}{
		{render.FormatTTY, true, "\x1b[31mred\x1b[m\n"},
		{render.FormatTTY, false, "red\n"},
		{render.FormatPlain, true, "red\n"},
	}
	for _, tt := range tests {
		var stdout bytes.Buffer
		out := filepath.Join(dir, string(tt.format)+".txt")
		s := render.Sink{
			Stdout:  &stdout,
			Format:  tt.format,
			Color:   tt.color,
			Outputs: []render.Output{{Format: render.FormatPlain, Path: out}},
		}
		require.NoError(t, s.Write(write))
		assert.Equal(t, tt.want, stdout.String(), "%s color=%v", tt.format, tt.color)
		b, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.Equal(t, "red\n", string(b), "--out files never get styles")
	}
}
