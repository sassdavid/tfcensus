package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contentSpec is the spec whose structure tables list every page of the site.
const contentSpec = "docs/plan/specs/docs-content.md"

// specPathCell finds the Path column of a structure table row: the second cell, one path in
// backticks.
var specPathCell = regexp.MustCompile("^\\|[^|]+\\|\\s*`(/[^`]*)`\\s*\\|")

// specPages returns the site paths in the structure tables of the content spec, except the
// generated CLI tree, which the gendocs tests cover.
func specPages(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, contentSpec))
	require.NoError(t, err)
	structure, _, ok := strings.Cut(string(b), "\n## Gaps")
	require.True(t, ok, "%s has no Gaps section after the structure", contentSpec)
	var out []string
	for line := range strings.SplitSeq(structure, "\n") {
		m := specPathCell.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[1], "/cli/") {
			continue
		}
		out = append(out, m[1])
	}
	require.NotEmpty(t, out, "no page paths found in %s", contentSpec)
	return out
}

// TestContentPages checks that every page in the content spec exists with a description and
// has a sidebar entry (docs-content criterion 1).
func TestContentPages(t *testing.T) {
	t.Parallel()
	sidebar, err := os.ReadFile(filepath.Join(repoRoot, "docs", ".vitepress", "sidebar.mts"))
	require.NoError(t, err)
	for _, page := range specPages(t) {
		file := strings.TrimPrefix(page, "/") + ".md"
		if page == "/" {
			file = "index.md"
		}
		b, rerr := os.ReadFile(filepath.Join(repoRoot, "docs", file))
		if errors.Is(rerr, fs.ErrNotExist) {
			t.Errorf("%s: the spec lists this page, but docs/%s doesn't exist", page, file)
			continue
		}
		require.NoError(t, rerr)
		front, _, _ := strings.Cut(strings.TrimPrefix(string(b), "---\n"), "\n---\n")
		assert.Regexp(t, `(?m)^description: \S`, front, "%s needs a description", file)
		if page != "/" {
			assert.Contains(t, string(sidebar), `link: "`+page+`"`, "%s has no sidebar entry", page)
		}
	}
}
