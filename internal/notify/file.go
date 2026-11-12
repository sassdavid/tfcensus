package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// FileConfig configures [File].
type FileConfig struct {
	Dir     string
	Formats []string // md and json
	DryRun  bool
}

// File writes tfcensus-<kind>.md and tfcensus-<kind>.json for any other system to pick up.
// A file whose content didn't change is left alone.
type File struct {
	cfg FileConfig
}

// NewFile returns a file notifier.
func NewFile(cfg FileConfig) *File {
	if cfg.Dir == "" {
		cfg.Dir = "."
	}
	if len(cfg.Formats) == 0 {
		cfg.Formats = []string{"md", "json"}
	}
	return &File{cfg: cfg}
}

// Name returns file.
func (f *File) Name() string { return NameFile }

// Publish writes the files.
func (f *File) Publish(_ context.Context, r *Report) (*Result, error) {
	res := &Result{Notifier: NameFile, Target: f.cfg.Dir, DryRun: f.cfg.DryRun, Actions: []Action{}}
	for _, format := range []string{"md", "json"} {
		if !slices.Contains(f.cfg.Formats, format) {
			continue
		}
		var content []byte
		if format == "md" {
			content = []byte(ReportMarkdown(r, MaxBody))
		} else {
			b, err := json.MarshalIndent(r, "", "  ")
			if err != nil {
				return res, fmt.Errorf("encode report: %w", err)
			}
			content = b
			content = append(content, '\n')
		}
		name := filepath.Join(f.cfg.Dir, "tfcensus-"+string(r.Kind)+"."+format)
		a := Action{Op: OpWrite, Kind: r.Kind, Scope: "all", Title: name, Detail: fmt.Sprintf("%d bytes", len(content))}
		old, err := os.ReadFile(name)
		switch {
		case err == nil && bytes.Equal(old, content):
			a.Op = OpUnchanged
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return res, fmt.Errorf("read %s: %w", name, err)
		case !f.cfg.DryRun:
			if err = writeAtomic(name, content); err != nil {
				return res, err
			}
		}
		res.add(a)
	}
	return res, nil
}

// writeAtomic writes through a temporary file in the same directory, so a reader never sees half
// a file. [os.Rename] replaces an existing file on Windows too.
func writeAtomic(name string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(name), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	_, werr := tmp.Write(content)
	cerr := tmp.Close()
	if err = errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err = os.Rename(tmp.Name(), name); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
