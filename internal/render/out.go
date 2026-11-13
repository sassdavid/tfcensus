package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	Outputs    []Output
	MDMaxChars int    // 0 means no limit
	Command    string // named in the truncation footer, such as "tfcensus drift"
}

// Write renders the report once per target. Markdown is cut to MDMaxChars.
func (s Sink) Write(write WriteFunc) error {
	if s.Format == FormatMD {
		var buf bytes.Buffer
		if err := write(&buf, s.Format); err != nil {
			return err
		}
		if _, err := io.WriteString(s.Stdout, s.truncate(buf.String())); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	} else if err := write(s.Stdout, s.Format); err != nil {
		return err
	}
	for _, o := range s.Outputs {
		var buf bytes.Buffer
		if err := write(&buf, o.Format); err != nil {
			return err
		}
		b := buf.Bytes()
		if o.Format == FormatMD {
			b = []byte(s.truncate(buf.String()))
		}
		if err := writeFile(o.Path, b); err != nil {
			return fmt.Errorf("write --out %s: %w", o.Path, err)
		}
	}
	return nil
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
