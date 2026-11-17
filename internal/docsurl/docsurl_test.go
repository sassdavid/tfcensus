package docsurl_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/docsurl"
)

// distEnv names the built site to check paths and anchors against, as docs:build sets it. Without
// it the test checks the Markdown sources.
const distEnv = "DOCS_DIST"

const repoRoot = "../.."

// templatePlaceholder is how the config template writes [docsurl.Base].
var templatePlaceholder = regexp.MustCompile(`\{\{docs\}\}/([^\s)"']*[^\s)"'.,;:])`)

// linkFuncs are the calls whose first argument is a site path: [docsurl.Page] and the cli helper
// that records related pages on a command.
func linkFuncs() []string { return []string{"Page", "setRelated"} }

// sitePaths returns every site path the code links to, with the file that names it.
func sitePaths(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(p string, e fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case e.IsDir() && (e.Name() == "testdata" || e.Name() == "testutil"):
				return filepath.SkipDir
			case strings.HasSuffix(p, ".yaml"):
				templatePaths(t, p, out)
			case strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go"):
				goPaths(t, fset, p, out)
			}
			return nil
		})
		require.NoError(t, err)
	}
	return out
}

// templatePaths adds the {{docs}}/path links of a YAML template to out.
func templatePaths(t *testing.T, p string, out map[string]string) {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	for _, m := range templatePlaceholder.FindAllSubmatch(b, -1) {
		out[string(m[1])] = p
	}
}

// goPaths adds the string literals passed to the link functions in a Go file to out.
func goPaths(t *testing.T, fset *token.FileSet, p string, out map[string]string) {
	t.Helper()
	f, err := parser.ParseFile(fset, p, nil, 0)
	require.NoError(t, err)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !slices.Contains(linkFuncs(), calledName(call)) {
			return true
		}
		page := calledName(call) == "Page"
		if page && !isDocsurl(call) {
			return true
		}
		for _, arg := range call.Args {
			lit, isLit := arg.(*ast.BasicLit)
			switch {
			case isLit && lit.Kind == token.STRING:
				v, _ := strconv.Unquote(lit.Value)
				out[v] = fset.Position(lit.Pos()).String()
			case page:
				t.Errorf("%s: docsurl.Page needs a string literal", fset.Position(arg.Pos()))
			}
		}
		return true
	})
}

func calledName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

func isDocsurl(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "docsurl"
}

// sourceExists reports whether the docs sources have the page or public file at path.
func sourceExists(path string) bool {
	docs := filepath.Join(repoRoot, "docs")
	if filepath.Ext(path) != "" {
		_, err := os.Stat(filepath.Join(docs, "public", path))
		return err == nil
	}
	path = strings.TrimSuffix(path, "/")
	candidates := []string{path + ".md", filepath.Join(path, "index.md"), filepath.Join(path, "README.md")}
	if path == "" {
		candidates = []string{"index.md"}
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(docs, c)); err == nil {
			return true
		}
	}
	return false
}

// distFile returns the built file for path, or "" when the site has none.
func distFile(dist, path string) string {
	path = strings.TrimSuffix(path, "/")
	for _, c := range []string{path, path + ".html", filepath.Join(path, "index.html")} {
		if st, err := os.Stat(filepath.Join(dist, c)); err == nil && !st.IsDir() {
			return filepath.Join(dist, c)
		}
	}
	return ""
}

// TestPaths checks that every site path the code links to is a page of the site, and with
// DOCS_DIST set, that the built page has the anchor.
func TestPaths(t *testing.T) {
	t.Parallel()
	paths := sitePaths(t)
	require.NotEmpty(t, paths)
	dist := os.Getenv(distEnv)
	for p, where := range paths {
		page, anchor, _ := strings.Cut(p, "#")
		assert.True(t, sourceExists(page), "%s: %s has no page under docs/", where, docsurl.Page(p))
		if dist == "" {
			continue
		}
		file := distFile(dist, page)
		if !assert.NotEmpty(t, file, "%s: %s is not in the built site", where, docsurl.Page(p)) || anchor == "" {
			continue
		}
		b, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Contains(t, string(b), `id="`+anchor+`"`, "%s: %s has no anchor #%s", where, page, anchor)
	}
}

func TestPage(t *testing.T) {
	t.Parallel()
	assert.Equal(t, docsurl.Base+"/reference/config", docsurl.Page("reference/config"))
	assert.Equal(t, docsurl.Base+"/reference/config", docsurl.Page("/reference/config"))
	assert.Equal(t, docsurl.Base+"/schemas/config-v1.json", docsurl.Schema("config-v1.json"))
}
