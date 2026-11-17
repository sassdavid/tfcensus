package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
)

// TestGenerated checks that the committed files match what gensite writes now, so a change to a
// pack, check, preset, layer or typemap entry without mise run docs fails go test too.
func TestGenerated(t *testing.T) {
	t.Parallel()
	files, err := generate(repoRoot)
	require.NoError(t, err)
	for name, want := range files {
		have, rerr := os.ReadFile(filepath.Join(repoRoot, name))
		if assert.NoError(t, rerr, "%s is missing; run mise run docs", name) {
			assert.Equal(t, string(want), string(have), "%s is stale; run mise run docs", name)
		}
	}
	parts, err := filepath.Glob(filepath.Join(repoRoot, "docs", "parts", "*"))
	require.NoError(t, err)
	for _, p := range parts {
		rel, _ := filepath.Rel(repoRoot, p)
		_, ok := files[filepath.ToSlash(rel)]
		assert.True(t, ok, "%s is not generated any more; delete it", rel)
	}
}

// ruleLiteral finds string literals in Go code that are rule IDs or rule ID prefixes.
var ruleLiteral = regexp.MustCompile(`"((?:match|inherit|source|rules|whois|pack|guard|user|preset)/[a-z0-9/.-]*)"`)

// TestRuleIDs checks that every rule ID literal in the non-test sources is on the rule IDs page,
// as an exact ID or as the prefix of a pattern such as user/<name>.
func TestRuleIDs(t *testing.T) {
	t.Parallel()
	var listed []string
	for _, r := range ruleIDs() {
		listed = append(listed, r.id)
	}
	err := filepath.WalkDir(filepath.Join(repoRoot, "internal"), func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") ||
			strings.Contains(p, "testutil") {
			return err
		}
		b, rerr := os.ReadFile(p)
		require.NoError(t, rerr)
		for _, m := range ruleLiteral.FindAllStringSubmatch(string(b), -1) {
			id := m[1]
			found := slices.ContainsFunc(listed, func(l string) bool {
				prefix, _, isPattern := strings.Cut(l, "<")
				return l == id || isPattern && id == prefix
			})
			assert.True(t, found, "%s: rule %s is not in ruleIDs", p, id)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestCELDocs(t *testing.T) {
	t.Parallel()
	docs := celDocs()
	fields := celFields()
	for _, f := range fields {
		assert.Contains(t, docs, f.path, "celDocs needs a description of %s", f.path)
	}
	for path := range docs {
		assert.True(t, slices.ContainsFunc(fields, func(f celField) bool { return f.path == path }),
			"celDocs describes %s, which rules can't read", path)
	}
}

func TestLayerDocs(t *testing.T) {
	t.Parallel()
	docs := layerDocs()
	assert.Len(t, docs, len(inventory.LayerOrder()))
	for _, l := range inventory.LayerOrder() {
		assert.Contains(t, docs, l)
	}
}

// TestMoveRules checks moveRules against the MV- rules in internal/migrate.
func TestMoveRules(t *testing.T) {
	t.Parallel()
	rule := regexp.MustCompile(`"(MV-[A-Z-]+)"`)
	files, err := filepath.Glob(filepath.Join(repoRoot, "internal", "migrate", "*.go"))
	require.NoError(t, err)
	var inCode []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		for _, m := range rule.FindAllStringSubmatch(string(b), -1) {
			inCode = append(inCode, m[1])
		}
	}
	var listed []string
	for _, r := range moveRules() {
		listed = append(listed, r[0])
	}
	slices.Sort(inCode)
	slices.Sort(listed)
	assert.Equal(t, slices.Compact(inCode), listed)
}

func TestSlug(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "audit-policy-noncurrent-days", slug("audit.policy.noncurrent_days"))
	assert.Equal(t, "pack-aws-defaults-default-vpc", slug("pack/aws-defaults/default-vpc"))
}

func TestCell(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `a \| b &lt;c&gt; s3:\*`, cell("a | b <c> s3:*"))
	assert.Equal(t, "````text\nx ``` y\n````\n", fenced("text", "x ``` y"))
}
