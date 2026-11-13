package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	out := flag.String("out", filepath.Join("docs", "cli"), "output directory")
	man := flag.String("man", "", "write gzipped man pages to this directory instead of Markdown to -out")
	flag.Parse()
	if *man != "" {
		if err := generateMan(*man); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gendocs: %v\n", err)
			return 1
		}
		return 0
	}
	stale, err := generate(*out)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(os.Stderr, "gendocs: %v\n", err)
		return 1
	case stale:
		_, _ = fmt.Fprintf(os.Stderr, "%s was stale and has been regenerated; review and commit it\n", *out)
		return 1
	default:
		return 0
	}
}

func generate(out string) (bool, error) {
	tmp, err := os.MkdirTemp("", "tfcensus-docs-")
	if err != nil {
		return false, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err = doc.GenMarkdownTree(newRoot(), tmp); err != nil {
		return false, fmt.Errorf("generate markdown: %w", err)
	}
	want, err := readDir(tmp)
	if err != nil {
		return false, err
	}
	have, err := readDir(out)
	if err != nil {
		return false, err
	}
	if equal(want, have) {
		return false, nil
	}
	if err = os.RemoveAll(out); err != nil {
		return false, fmt.Errorf("clear %s: %w", out, err)
	}
	if err = os.MkdirAll(out, 0o750); err != nil {
		return false, fmt.Errorf("create %s: %w", out, err)
	}
	for name, b := range want {
		if err = os.WriteFile(filepath.Join(out, name), b, 0o600); err != nil {
			return false, fmt.Errorf("write %s: %w", name, err)
		}
	}
	return true, nil
}

func newRoot() *cobra.Command {
	return cli.NewRootCmd(&app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Clock:  time.Now,
		Getwd:  os.Getwd,
	})
}

// generateMan writes one gzipped man page per command to out. cobra dates the pages from
// SOURCE_DATE_EPOCH, and the gzip header carries no name or time, so a rerun writes the same bytes.
func generateMan(out string) error {
	tmp, err := os.MkdirTemp("", "tfcensus-man-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err = doc.GenManTree(newRoot(), &doc.GenManHeader{Section: "1"}, tmp); err != nil {
		return fmt.Errorf("generate man pages: %w", err)
	}
	pages, err := readDir(tmp)
	if err != nil {
		return err
	}
	if err = os.RemoveAll(out); err != nil {
		return fmt.Errorf("clear %s: %w", out, err)
	}
	if err = os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	for name, b := range pages {
		var buf bytes.Buffer
		zw, zerr := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if zerr != nil {
			return fmt.Errorf("compress %s: %w", name, zerr)
		}
		if _, err = zw.Write(b); err != nil {
			return fmt.Errorf("compress %s: %w", name, err)
		}
		if err = zw.Close(); err != nil {
			return fmt.Errorf("compress %s: %w", name, err)
		}
		if err = os.WriteFile(filepath.Join(out, name+".gz"), buf.Bytes(), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func readDir(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	files := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), readErr)
		}
		files[e.Name()] = b
	}
	return files, nil
}

func equal(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	names := make([]string, 0, len(a))
	for n := range a {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if !bytes.Equal(a[n], b[n]) {
			return false
		}
	}
	return true
}
