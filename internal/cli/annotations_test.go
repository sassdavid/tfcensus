package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/render"
)

const (
	formatRejected = "is not supported by this command"
	failOnRejected = "isn't checked by"
)

// runnable returns every command that has a page on the docs site and runs something.
func runnable(t *testing.T) []*cobra.Command {
	t.Helper()
	d, _, _ := newDeps(t)
	var out []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() {
			out = append(out, c)
		}
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() {
				walk(sub)
			}
		}
	}
	walk(cli.NewRootCmd(d))
	require.NotEmpty(t, out)
	return out
}

// path returns the command path without the program name.
func path(c *cobra.Command) []string {
	return strings.Fields(c.CommandPath())[1:]
}

// TestAnnotations_present checks that every runnable command records its formats and exit codes,
// and that a command with --fail-on records the values its completion offers.
func TestAnnotations_present(t *testing.T) {
	t.Parallel()
	for _, c := range runnable(t) {
		name := c.CommandPath()
		_, ok := c.Annotations[cli.AnnotationFormats]
		assert.True(t, ok, "%s: no %s annotation", name, cli.AnnotationFormats)
		for _, f := range cli.SplitAnnotation(c, cli.AnnotationFormats) {
			assert.Contains(t, render.AllFormats(), render.Format(f), "%s: unknown format", name)
		}
		codes := cli.SplitAnnotation(c, cli.AnnotationExitCodes)
		assert.NotEmpty(t, codes, "%s: no %s annotation", name, cli.AnnotationExitCodes)
		known := map[string]bool{}
		for _, e := range cli.ExitCodes() {
			known[strconv.Itoa(e.Code)] = true
		}
		for _, code := range codes {
			assert.True(t, known[code], "%s: exit code %s is not in cli.ExitCodes", name, code)
		}
		if f, hasFailOn := c.GetFlagCompletionFunc("fail-on"); hasFailOn {
			assert.Equal(t, complete.Values(complete.StaticValues(c, f)),
				cli.SplitAnnotation(c, cli.AnnotationFailOn), "%s: fail-on annotation", name)
		} else {
			assert.Empty(t, c.Annotations[cli.AnnotationFailOn], "%s: fail-on annotation without the flag", name)
		}
	}
}

// invocation returns args that get c past argument and required-flag validation, so the run
// reaches the root's flag checks.
func invocation(t *testing.T, c *cobra.Command) []string {
	t.Helper()
	args := path(c)
	if c.Args != nil {
		var pos []string
		for len(pos) < 4 && c.Args(c, pos) != nil {
			pos = append(pos, "x")
		}
		args = append(args, pos...)
	}
	set := map[string]bool{}
	add := func(f *pflag.Flag) {
		if set[f.Name] {
			return
		}
		set[f.Name] = true
		switch f.Value.Type() {
		case "bool":
			args = append(args, "--"+f.Name)
		case "int", "int64", "uint", "float64", "count":
			args = append(args, "--"+f.Name, "1")
		case "duration":
			args = append(args, "--"+f.Name, "1s")
		default:
			args = append(args, "--"+f.Name, "x")
		}
	}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if v := f.Annotations[cobra.BashCompOneRequiredFlag]; len(v) == 1 && v[0] == "true" {
			add(f)
		}
	})
	c.Flags().VisitAll(func(f *pflag.Flag) {
		for _, group := range f.Annotations["cobra_annotation_one_required"] {
			names := strings.Fields(group)
			if !slices.ContainsFunc(names, func(n string) bool { return set[n] }) {
				add(c.Flags().Lookup(names[0]))
			}
		}
	})
	return args
}

// runInvalidConfig runs args with a config file that fails to parse, so a command that gets past
// the flag checks stops before it reads units, calls a service or writes anything.
func runInvalidConfig(t *testing.T, args ...string) (int, string) {
	t.Helper()
	d, _, stderr := newDeps(t)
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("schema_version: [\n"), 0o600))
	code := cli.Execute(t.Context(), d, append(args, "--config", bad))
	return code, stderr.String()
}

// TestAnnotations_formats runs every command that writes a report with every format: the
// recorded ones pass the format check, and the others exit 2. Commands that ignore --format are
// left out: they never check it, and the completion shells change package-level state when run.
func TestAnnotations_formats(t *testing.T) {
	t.Parallel()
	for _, c := range runnable(t) {
		args := invocation(t, c)
		formats := cli.SplitAnnotation(c, cli.AnnotationFormats)
		if len(formats) == 0 {
			continue
		}
		for _, f := range render.AllFormats() {
			t.Run(strings.Join(path(c), "_")+"/"+string(f), func(t *testing.T) {
				t.Parallel()
				code, stderr := runInvalidConfig(t, append(slices.Clone(args), "--format", string(f))...)
				if slices.Contains(formats, string(f)) {
					assert.NotContains(t, stderr, formatRejected)
					return
				}
				assert.Equal(t, cli.ExitUsage, code, stderr)
				assert.Contains(t, stderr, formatRejected)
			})
		}
	}
}

// TestAnnotations_failOn runs every command that has --fail-on with each recorded value, which
// passes the check, and with an unknown value, which exits 2.
func TestAnnotations_failOn(t *testing.T) {
	t.Parallel()
	for _, c := range runnable(t) {
		values := cli.SplitAnnotation(c, cli.AnnotationFailOn)
		if len(values) == 0 {
			continue
		}
		args := invocation(t, c)
		for _, v := range append(slices.Clone(values), "no-such-gate") {
			t.Run(strings.Join(path(c), "_")+"/"+v, func(t *testing.T) {
				t.Parallel()
				code, stderr := runInvalidConfig(t, append(slices.Clone(args), "--fail-on", v)...)
				if slices.Contains(values, v) {
					assert.NotContains(t, stderr, failOnRejected)
					return
				}
				assert.Equal(t, cli.ExitUsage, code, stderr)
				assert.Contains(t, stderr, failOnRejected)
			})
		}
	}
}

// specificExitCodes maps the exit code constants a command can return on its own to their values.
// The other codes are recorded on every command.
func specificExitCodes() map[string]int {
	return map[string]int{"ExitFindings": cli.ExitFindings, "ExitAuth": cli.ExitAuth}
}

// codeGraph is the package's functions and methods by name, with the specific exit codes each
// one names and the functions it calls. Calls are matched by name only, so a name shared by two
// functions counts for both.
type codeGraph struct {
	decls  map[string][]*ast.FuncDecl
	direct map[string]map[int]bool
	calls  map[string]map[string]bool
}

func newCodeGraph(t *testing.T) *codeGraph {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	g := &codeGraph{
		decls:  map[string][]*ast.FuncDecl{},
		direct: map[string]map[int]bool{},
		calls:  map[string]map[string]bool{},
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, perr)
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok {
				g.decls[fd.Name.Name] = append(g.decls[fd.Name.Name], fd)
			}
		}
	}
	for name, fds := range g.decls {
		g.direct[name], g.calls[name] = map[int]bool{}, map[string]bool{}
		for _, fd := range fds {
			g.scan(fd.Body, g.direct[name], g.calls[name])
		}
	}
	return g
}

// scan adds the exit codes that n names to codes and the package functions it calls to calls.
// A selector x.f counts as a call of the method f, and a plain name f as a call of the function f.
func (g *codeGraph) scan(n ast.Node, codes map[int]bool, calls map[string]bool) {
	if n == nil {
		return
	}
	sels := map[*ast.Ident]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			sels[n.Sel] = true
		case *ast.Ident:
			if code, isCode := specificExitCodes()[n.Name]; isCode {
				codes[code] = true
			}
			for _, fd := range g.decls[n.Name] {
				if (fd.Recv != nil) == sels[n] {
					calls[n.Name] = true
				}
			}
		}
		return true
	})
}

// reachable returns the exit codes that n names, directly or through the functions it calls.
func (g *codeGraph) reachable(n ast.Node) map[int]bool {
	codes, calls := map[int]bool{}, map[string]bool{}
	g.scan(n, codes, calls)
	seen := map[string]bool{}
	var queue []string
	for c := range calls {
		seen[c] = true
		queue = append(queue, c)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for code := range g.direct[name] {
			codes[code] = true
		}
		for callee := range g.calls[name] {
			if !seen[callee] {
				seen[callee] = true
				queue = append(queue, callee)
			}
		}
	}
	return codes
}

// body returns the code behind a function value named as runtime reports it: the function or
// method itself, or the closure "outer.funcN" (the Nth function literal in outer, in source
// order).
func (g *codeGraph) body(t *testing.T, name string) ast.Node {
	t.Helper()
	outer, rest, _ := strings.Cut(name, ".")
	fds := g.decls[outer]
	require.Len(t, fds, 1, "function %s", outer)
	if rest == "" {
		return fds[0].Body
	}
	first, _, _ := strings.Cut(rest, ".")
	n, err := strconv.Atoi(strings.TrimPrefix(first, "func"))
	require.NoError(t, err, "closure %s", name)
	var lits []*ast.FuncLit
	ast.Inspect(fds[0].Body, func(node ast.Node) bool {
		if lit, ok := node.(*ast.FuncLit); ok {
			lits = append(lits, lit)
			return false
		}
		return true
	})
	require.GreaterOrEqual(t, len(lits), n, "closure %s", name)
	return lits[n-1]
}

// funcName returns the name runtime reports for fn without the package path, such as
// newScanCmd.func1 or runScan, or "" when fn is outside package cli.
func funcName(fn any) string {
	name := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	const pkg = "/internal/cli."
	i := strings.LastIndex(name, pkg)
	if i < 0 {
		return ""
	}
	name = strings.TrimSuffix(name[i+len(pkg):], "-fm")
	if strings.HasPrefix(name, "(") {
		_, name, _ = strings.Cut(name, ").")
	}
	return name
}

// exitCodeExceptions names commands, by path below the root, whose code reaches an exit code
// constant that the command still never returns, with the reason. Their annotation may list
// fewer codes than the code reaches.
func exitCodeExceptions() map[string]string {
	return map[string]string{
		"mcp": "a tool's error goes back to the client as a tool result; the server never exits 3 or 4",
	}
}

// TestAnnotations_exitCodes checks that each command records exactly the specific exit codes
// (3 and 4) that the code behind its RunE can return.
func TestAnnotations_exitCodes(t *testing.T) {
	t.Parallel()
	g := newCodeGraph(t)
	for _, c := range runnable(t) {
		var fn any = c.RunE
		if c.RunE == nil {
			fn = c.Run
		}
		var want []string
		if name := funcName(fn); name != "" {
			for code := range g.reachable(g.body(t, name)) {
				want = append(want, strconv.Itoa(code))
			}
		}
		var got []string
		for _, code := range cli.SplitAnnotation(c, cli.AnnotationExitCodes) {
			if n, _ := strconv.Atoi(code); slices.Contains([]int{cli.ExitFindings, cli.ExitAuth}, n) {
				got = append(got, code)
			}
		}
		slices.Sort(want)
		if _, ok := exitCodeExceptions()[strings.Join(path(c), " ")]; ok {
			assert.Subset(t, want, got, "%s: specific exit codes", c.CommandPath())
			continue
		}
		assert.Equal(t, want, got, "%s: specific exit codes", c.CommandPath())
	}
}

// helpTextAllowlist reports whether c may have no Long or Example text: the completion shells
// share one page, which lists them in a table.
func helpTextAllowlist(c *cobra.Command) bool {
	return c.HasParent() && c.Parent().Name() == "completion"
}

// TestHelpTexts checks that every command with a docs page has a Long and an Example text.
func TestHelpTexts(t *testing.T) {
	t.Parallel()
	d, _, _ := newDeps(t)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if !helpTextAllowlist(c) {
			assert.NotEmpty(t, strings.TrimSpace(c.Long), "%s has no Long text", c.CommandPath())
			assert.NotEmpty(t, strings.TrimSpace(c.Example), "%s has no Example text", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() {
				walk(sub)
			}
		}
	}
	walk(cli.NewRootCmd(d))
}
