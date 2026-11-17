package main

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const repoRoot = "../.."

// distEnv names the built site, as docs:build sets it. Without it only the sources are checked.
const distEnv = "DOCS_DIST"

// unpublished are the docs paths VitePress leaves out (srcExclude in config.mts).
func unpublished() []string {
	return []string{
		"plan", "engineering", "validation", "dogfood.md", "README.md", "iam/README.md",
		"node_modules", ".vitepress/dist", ".vitepress/cache",
	}
}

// fixtureSecrets are attribute values of the fixture states that the CLI grep tests look for.
// None may reach the site, not even through an output included from a golden.
func fixtureSecrets() []string {
	return []string{
		"FIXTURE-SECRET", "RklYVFVSRS1TRUNSRVQ", "10.20.0.0/16", "10.40.0.0/16", "10.42.0.0/16",
		"10.77.0.0/16", "acme_admin", "AROAFIXTURE", "ci@runner", "db.t4g.small", "SecureString",
	}
}

// fixtureAccounts are the account IDs the fixtures and examples use. Any other 12-digit number
// could be a real account.
func fixtureAccounts() []string {
	return []string{
		"000000000001", "000000000002", "000000000003", "111111111111", "222222222222", "333333333333",
		"123456789012", "999999999999",
	}
}

var (
	tokenPattern = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}|` +
		`xox[abprs]-[A-Za-z0-9-]{10,}|hooks\.slack\.com/services/T[A-Za-z0-9]+|AKIA[0-9A-Z]{16}|` +
		`ASIA[0-9A-Z]{16}`)
	accountPattern = regexp.MustCompile(`\b\d{12}\b`)
	linkTarget     = regexp.MustCompile(`(?:\]\(|href=")([^)"\s]+)`)
	// internalTarget matches a link target in the unpublished docs, relative or on GitHub.
	internalTarget = regexp.MustCompile(`(?:^|/)(?:plan|engineering|validation)/|dogfood\.md|CLAUDE\.md|\.claude/rules`)
	svgPath        = regexp.MustCompile(` d="[^"]*"`)
	milestone      = regexp.MustCompile(`\bM[0-9]{1,2}\b`)
	// profileFlag finds the AWS profile names that the unpublished validation notes used.
	profileFlag = regexp.MustCompile(`(?:--profile[ =]|AWS_PROFILE=)([A-Za-z0-9_.-]+)`)
)

// testOnly are names of test harness internals that no user page mentions.
func testOnly() []string {
	return []string{"TEST_CASSETTE", "GOLDEN_UPDATE", "TEST_NOW", "fakebin", "_carapace", "testdata/cassettes"}
}

// realProfiles returns the profile names in the unpublished notes, except the fixture ones.
func realProfiles(t *testing.T) []string {
	t.Helper()
	var out []string
	files := []string{filepath.Join(repoRoot, "docs", "dogfood.md")}
	more, err := filepath.Glob(filepath.Join(repoRoot, "docs", "validation", "*.md"))
	require.NoError(t, err)
	for _, f := range append(files, more...) {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		for _, m := range profileFlag.FindAllStringSubmatch(string(b), -1) {
			if !strings.HasPrefix(m[1], "acme-") && !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	require.NotEmpty(t, out, "the notes name the profile they ran with; the check needs it")
	return out
}

// siteSources returns the published files under docs/ and the files they include, keyed by
// their path relative to the repository root.
func siteSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	docs := filepath.Join(repoRoot, "docs")
	err := filepath.WalkDir(docs, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(docs, p)
		if slices.Contains(unpublished(), filepath.ToSlash(rel)) {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".md", ".txt", ".json", ".vue", ".mts", ".yml":
			if !e.IsDir() {
				b, rerr := os.ReadFile(p)
				require.NoError(t, rerr)
				out["docs/"+filepath.ToSlash(rel)] = string(b)
			}
		}
		return nil
	})
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(repoRoot, "CHANGELOG.md"))
	require.NoError(t, err)
	out["CHANGELOG.md"] = string(b)
	return out
}

// distFiles returns the text files of the built site.
func distFiles(t *testing.T, dist string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dist, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		switch filepath.Ext(p) {
		case ".html", ".txt", ".json", ".xml":
			b, rerr := os.ReadFile(p)
			require.NoError(t, rerr)
			rel, _ := filepath.Rel(dist, p)
			out["dist/"+filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, out, "%s has no built pages", dist)
	return out
}

// TestNeverOnSite runs the checks of docs-content.md "Never on the site" over the published
// sources and, with DOCS_DIST set, over the built site: the state-attribute and token grep, real
// account IDs and profile names, links into internal docs, milestone names and test internals.
func TestNeverOnSite(t *testing.T) {
	t.Parallel()
	files := siteSources(t)
	if dist := os.Getenv(distEnv); dist != "" {
		maps.Copy(files, distFiles(t, dist))
	}
	profiles := realProfiles(t)
	for name, text := range files {
		checkNeverOnSite(t, name, text, profiles)
	}
}

// checkNeverOnSite reports everything in one file that must not reach the site.
func checkNeverOnSite(t *testing.T, name, text string, profiles []string) {
	t.Helper()
	for _, s := range fixtureSecrets() {
		assert.NotContains(t, text, s, "%s shows a fixture state value", name)
	}
	if m := tokenPattern.FindString(text); m != "" {
		t.Errorf("%s holds what looks like a token: %.12s...", name, m)
	}
	for _, p := range profiles {
		assert.NotContains(t, text, p, "%s names the AWS profile of the validation notes", name)
	}
	if !strings.HasSuffix(name, ".json") {
		for _, id := range accountPattern.FindAllString(text, -1) {
			assert.Contains(t, fixtureAccounts(), id, "%s has a 12-digit number that isn't a fixture account ID", name)
		}
	}
	if link := internalLink(text); link != "" {
		t.Errorf("%s links into the internal docs: %s", name, link)
	}
	if isChangelog(name) {
		// release-please writes the changelog from commit subjects, which name milestones.
		return
	}
	// SVG path data such as d="M2 11 ..." looks like a milestone name.
	if m := milestone.FindString(
		svgPath.ReplaceAllString(text, ""),
	); m != "" &&
		!strings.HasPrefix(name, "dist/assets/") {
		t.Errorf("%s names the milestone %s", name, m)
	}
	for _, s := range testOnly() {
		assert.NotContains(t, text, s, "%s mentions a test internal", name)
	}
}

func isChangelog(name string) bool {
	return name == "CHANGELOG.md" || name == "dist/changelog.html"
}

// internalLink returns the first link target in text that points into the unpublished docs.
func internalLink(text string) string {
	for _, m := range linkTarget.FindAllStringSubmatch(text, -1) {
		if internalTarget.MatchString(m[1]) {
			return m[1]
		}
	}
	return ""
}

func TestNeverOnSitePatterns(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "../plan/adr/0007.md", internalLink("[ADR](../plan/adr/0007.md)"))
	assert.NotEmpty(t, internalLink(`<a href="https://github.com/sassdavid/tfcensus/blob/main/docs/engineering/x.md">`))
	assert.NotEmpty(t, internalLink("[notes](https://github.com/sassdavid/tfcensus/blob/main/docs/dogfood.md)"))
	assert.Empty(t, internalLink("[config](/reference/config) and [plan](/cli/drift#plan)"))
	assert.Equal(t, []string{"111111111111"}, accountPattern.FindAllString("arn:aws:iam::111111111111:role/x", -1))
	assert.Empty(t, accountPattern.FindAllString("12bba5249b57df79c1d8e66b3f0e74dec664157e", -1))
	assert.Regexp(t, milestone, "since M19")
	assert.NotRegexp(t, milestone, "M5a and BM12")
	assert.Empty(t, svgPath.ReplaceAllString(` d="M2 11 L4 4"`, ""))
}
