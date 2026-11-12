package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// writeFuncs are the os functions that create, change or delete files, or start a process.
func writeFuncs() []string {
	return []string{
		"Create", "CreateTemp", "WriteFile", "OpenFile", "Mkdir", "MkdirAll", "MkdirTemp", "Remove",
		"RemoveAll", "Rename", "Symlink", "Link", "Chmod", "Chown", "Lchown", "Chtimes", "Truncate",
		"StartProcess", "CopyFS",
	}
}

// bannedImports are packages the MCP code may not import: process control, AWS clients and the
// packages that write state, S3 or DynamoDB.
func bannedImports() []string {
	return []string{
		"os/exec", "syscall",
		"github.com/sassdavid/tfcensus/internal/execx",
		"github.com/sassdavid/tfcensus/internal/migrate",
		"github.com/sassdavid/tfcensus/internal/accounts",
		"github.com/sassdavid/tfcensus/internal/discovery",
		"github.com/sassdavid/tfcensus/internal/notify",
		"github.com/sassdavid/tfcensus/internal/httpx",
	}
}

// bannedCalls are functions of package cli that start terragrunt, call AWS or move state.
func bannedCalls() []string {
	return []string{
		"newLoader", "discoverUnits", "readStates", "resolveAccounts", "collectLive", "whoisLive",
		"newNotifier", "runMove", "newScanEnv",
	}
}

// TestMCP_staticNoExecNoWrite reads the code behind tfcensus mcp and fails when a tool could start
// a process, call AWS, write a file or move state. The packages that serve MCP and the saved
// reader the tools run through may not import process, AWS or write packages, call an os
// function that writes, or call the cli functions that discover units, read states or move.
func TestMCP_staticNoExecNoWrite(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	files := []string{"../mcpserver/*.go", "../mcpkit/*.go", "../report/*.go", "mcp.go", "saved.go"}
	parsed := 0
	for _, pattern := range files {
		for _, f := range parseGo(t, fset, pattern) {
			parsed++
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if slices.Contains(bannedImports(), p) || strings.HasPrefix(p, "github.com/aws/") {
					t.Errorf("%s imports %s", fset.Position(imp.Pos()), p)
				}
			}
			checkNoExecNoWrite(t, fset, f)
		}
	}
	if parsed < 8 {
		t.Errorf("parsed %d files; did the MCP code move?", parsed)
	}
}

func parseGo(t *testing.T, fset *token.FileSet, pattern string) []*ast.File {
	t.Helper()
	names, err := filepath.Glob(filepath.FromSlash(pattern))
	if err != nil || len(names) == 0 {
		t.Fatalf("no files match %s: %v", pattern, err)
	}
	var out []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatal(perr)
		}
		out = append(out, f)
	}
	return out
}

func checkNoExecNoWrite(t *testing.T, fset *token.FileSet, n ast.Node) {
	t.Helper()
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "os" && slices.Contains(writeFuncs(), x.Sel.Name) {
				t.Errorf("%s calls os.%s", fset.Position(x.Pos()), x.Sel.Name)
			}
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && slices.Contains(bannedCalls(), id.Name) {
				t.Errorf("%s calls %s", fset.Position(x.Pos()), id.Name)
			}
		}
		return true
	})
}
