package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sassdavid/tfcensus/internal/term"
)

// Output is one --out value: a format and the file it goes to.
type Output struct {
	Format Format
	Path   string
}

// OutFormats lists the formats --out accepts. tty is left out because a file is never a terminal.
func OutFormats() []Format {
	return slices.DeleteFunc(AllFormats(), func(f Format) bool { return f == FormatTTY })
}

// ParseOutputs validates --out values of the form format=path.
func ParseOutputs(specs []string) ([]Output, error) {
	out := make([]Output, 0, len(specs))
	seen := map[string]bool{}
	for _, s := range specs {
		name, path, ok := strings.Cut(s, "=")
		if !ok || name == "" || path == "" {
			return nil, fmt.Errorf("invalid --out %q: want format=path, such as sarif=report.sarif", s)
		}
		f := Format(name)
		if !slices.Contains(OutFormats(), f) {
			return nil, fmt.Errorf("invalid --out format %q: want one of %s", name, joinFormats(OutFormats()))
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			return nil, fmt.Errorf("invalid --out %q: %s is already an --out file", s, path)
		}
		seen[clean] = true
		out = append(out, Output{Format: f, Path: path})
	}
	return out, nil
}

// WriteFunc writes one report in format f.
type WriteFunc func(w io.Writer, f Format) error

// Sink sends one report to stdout in the --format format and to every --out file.
type Sink struct {
	Stdout     io.Writer
	Format     Format
	Color      bool // keep the styles of the tty format on stdout
	Outputs    []Output
	MDMaxChars int    // 0 means no limit
	Command    string // named in the truncation footer, such as "tfcensus drift"
}

// Write renders the report once per target and writes each in one call. Styles reach stdout
// only in the tty format with Color; Markdown is cut to MDMaxChars. When stdout is a pipe that
// closed early, as with `| head`, the rest of stdout is dropped and the --out files are still
// written.
func (s Sink) Write(write WriteFunc) error {
	b, err := s.render(write, s.Format, s.Color && s.Format == FormatTTY)
	if err != nil {
		return err
	}
	if _, err = s.Stdout.Write(b); err != nil && !term.IsBrokenPipe(err) {
		return fmt.Errorf("write output: %w", err)
	}
	for _, o := range s.Outputs {
		if b, err = s.render(write, o.Format, false); err != nil {
			return err
		}
		if err = writeFile(o.Path, b); err != nil {
			return fmt.Errorf("write --out %s: %w", o.Path, err)
		}
	}
	return nil
}

// render writes the report in format f to memory. The text formats pass through a color
// profile writer, which drops the styles unless color is set.
func (s Sink) render(write WriteFunc, f Format, color bool) ([]byte, error) {
	var buf bytes.Buffer
	var w io.Writer = &buf
	if f == FormatTTY || f == FormatPlain {
		w = term.Writer(&buf, term.Stream{Color: color}.Profile())
	}
	if err := write(w, f); err != nil {
		return nil, err
	}
	if f == FormatMD {
		return []byte(s.truncate(buf.String())), nil
	}
	return buf.Bytes(), nil
}

func (s Sink) truncate(md string) string {
	hint := ""
	if s.Command != "" {
		hint = "Run `" + s.Command + " --format json` for the full report."
	}
	return TruncateMarkdown(md, s.MDMaxChars, hint)
}

// writeFile replaces path through a temporary file, so a reader never sees half a report.
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename temporary file: %w", err)
	}
	return nil
}
