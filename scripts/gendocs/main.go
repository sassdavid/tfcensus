package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
)

// Permissions of the folders and files gendocs creates.
const (
	dirPerm  = 0o750
	filePerm = 0o600
)

// ownedDirs are the folders under docs/ that gendocs writes in full; any other file in them is
// stale and removed.
func ownedDirs() []string { return []string{"cli", "public/schemas"} }

// ownedFiles are the single pages gendocs writes next to hand-written ones.
func ownedFiles() []string {
	return []string{
		"reference/config.md", "reference/environment.md", "reference/exit-codes.md",
		"reference/fail-on.md", "reference/mcp.md", "reference/schemas.md",
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	docs := flag.String("docs", "docs", "docs site directory to write the generated pages to")
	man := flag.String("man", "", "write gzipped man pages to this directory instead of the site pages")
	snippets := flag.Bool("snippets", false, "check the command lines and config keys in the site's Markdown")
	flag.Parse()
	ctx := context.Background()
	switch {
	case *man != "":
		if err := generateMan(*man); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gendocs: %v\n", err)
			return 1
		}
		return 0
	case *snippets:
		problems, err := checkSnippets(*docs, newRoot())
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "gendocs: %v\n", err)
			return 1
		}
		for _, p := range problems {
			_, _ = fmt.Fprintln(os.Stderr, p)
		}
		if len(problems) > 0 {
			return 1
		}
		return 0
	}
	files, err := generate(ctx, *docs)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gendocs: %v\n", err)
		return 1
	}
	stale, err := sync(*docs, files)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gendocs: %v\n", err)
		return 1
	}
	for _, f := range stale {
		_, _ = fmt.Fprintf(os.Stderr, "%s was stale and has been regenerated; review and commit it\n",
			filepath.Join(*docs, f))
	}
	if len(stale) > 0 {
		return 1
	}
	return 0
}

// generator collects the generated files, keyed by their path under docs/.
type generator struct {
	docs  string
	files map[string][]byte
}

func (g *generator) add(name string, b []byte) { g.files[name] = b }

var (
	titleKey = regexp.MustCompile(`(?m)^title:\s*"?([^"\n]+)"?\s*$`)
	heading1 = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

// pageTitle returns the title of the site page at link, from the generated files or the
// Markdown source, or the link itself when the page has none.
func (g *generator) pageTitle(link string) string {
	page, _, _ := strings.Cut(link, "#")
	page = strings.TrimSuffix(page, "/")
	for _, f := range []string{page + ".md", page + "/index.md", page + "/README.md"} {
		b, ok := g.files[f]
		if !ok {
			var err error
			if b, err = os.ReadFile(filepath.Join(g.docs, f)); err != nil {
				continue
			}
		}
		if title := titleOf(b); title != "" {
			return title
		}
	}
	return link
}

// titleOf returns the title key of a page's frontmatter, else its first # heading after the
// frontmatter. The comment line of a generated page's frontmatter also starts with #.
func titleOf(b []byte) string {
	body := b
	if rest, ok := bytes.CutPrefix(b, []byte("---\n")); ok {
		if front, after, found := bytes.Cut(rest, []byte("\n---\n")); found {
			if m := titleKey.FindSubmatch(front); m != nil {
				return string(bytes.TrimSpace(m[1]))
			}
			body = after
		}
	}
	if m := heading1.FindSubmatch(body); m != nil {
		return string(bytes.TrimSpace(m[1]))
	}
	return ""
}

// generate returns every generated file of the docs site.
func generate(ctx context.Context, docs string) (map[string][]byte, error) {
	g := &generator{docs: docs, files: map[string][]byte{}}
	root := newRoot()
	if err := g.configPage(); err != nil {
		return nil, err
	}
	g.environmentPage()
	g.exitCodesPage(root)
	g.failOnPage(root)
	if err := g.schemasPage(); err != nil {
		return nil, err
	}
	if err := g.mcpPage(ctx); err != nil {
		return nil, err
	}
	if err := g.cliPages(root); err != nil {
		return nil, err
	}
	return g.files, nil
}

// sync writes files under docs, removes stale files from the owned folders, and returns the
// paths it changed.
func sync(docs string, files map[string][]byte) ([]string, error) {
	if err := os.MkdirAll(docs, 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", docs, err)
	}
	root, err := os.OpenRoot(docs)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", docs, err)
	}
	defer func() { _ = root.Close() }()
	removed, err := removeStale(root, files)
	if err != nil {
		return nil, err
	}
	written, err := writeChanged(root, files)
	if err != nil {
		return nil, err
	}
	changed := slices.Concat(removed, written)
	slices.Sort(changed)
	return changed, nil
}

// removeStale deletes the files in the owned folders that files doesn't have.
func removeStale(root *os.Root, files map[string][]byte) ([]string, error) {
	var stale []string
	for _, dir := range ownedDirs() {
		err := fs.WalkDir(root.FS(), dir, func(p string, e fs.DirEntry, err error) error {
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return fs.SkipDir
			case err != nil || e.IsDir():
				return err
			}
			if _, ok := files[p]; !ok {
				stale = append(stale, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("clean %s: %w", dir, err)
		}
	}
	for _, p := range stale {
		if err := root.Remove(p); err != nil {
			return nil, fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return stale, nil
}

// writeChanged writes each file whose content differs from the one on disk.
func writeChanged(root *os.Root, files map[string][]byte) ([]string, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		if !owned(n) {
			return nil, fmt.Errorf("%s is outside the generated paths", n)
		}
		names = append(names, n)
	}
	slices.Sort(names)
	var written []string
	for _, n := range names {
		if have, err := root.ReadFile(n); err == nil && bytes.Equal(have, files[n]) {
			continue
		}
		if err := root.MkdirAll(path.Dir(n), dirPerm); err != nil {
			return nil, fmt.Errorf("create %s: %w", path.Dir(n), err)
		}
		if err := root.WriteFile(n, files[n], filePerm); err != nil {
			return nil, fmt.Errorf("write %s: %w", n, err)
		}
		written = append(written, n)
	}
	return written, nil
}

func owned(path string) bool {
	if slices.Contains(ownedFiles(), path) {
		return true
	}
	return slices.ContainsFunc(ownedDirs(), func(d string) bool { return strings.HasPrefix(path, d+"/") })
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
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return fmt.Errorf("read %s: %w", tmp, err)
	}
	if err = os.RemoveAll(out); err != nil {
		return fmt.Errorf("clear %s: %w", out, err)
	}
	if err = os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	for _, e := range entries {
		b, rerr := os.ReadFile(filepath.Join(tmp, e.Name()))
		if rerr != nil {
			return fmt.Errorf("read %s: %w", e.Name(), rerr)
		}
		var buf bytes.Buffer
		zw, zerr := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if zerr != nil {
			return fmt.Errorf("compress %s: %w", e.Name(), zerr)
		}
		if _, err = zw.Write(b); err != nil {
			return fmt.Errorf("compress %s: %w", e.Name(), err)
		}
		if err = zw.Close(); err != nil {
			return fmt.Errorf("compress %s: %w", e.Name(), err)
		}
		if err = os.WriteFile(filepath.Join(out, e.Name()+".gz"), buf.Bytes(), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}
	return nil
}
