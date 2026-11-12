package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// listAnnotation is the comment the provider puts on a list resource's factory.
var listAnnotation = regexp.MustCompile(`@(?:SDK|Framework)ListResource\("([a-z0-9_]+)"\)`)

// listRequired returns, for each list resource of the service package in dir, the attributes
// its list config marks Required. A list block without them fails terraform validate.
func listRequired(dir string, attrs map[string]string) (map[string][]string, error) {
	files, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	local := maps.Clone(attrs)               // plus the package's own string constants
	factories := map[string]string{}         // struct type to TF type
	schemas := map[string][]*ast.BlockStmt{} // struct type to its list config schema bodies
	for _, f := range files {
		for _, decl := range f.Decls {
			collectDecl(decl, local, factories, schemas)
		}
	}
	out := map[string][]string{}
	for _, st := range slices.Sorted(maps.Keys(factories)) {
		for _, body := range schemas[st] {
			if req := requiredAttrs(body, local); len(req) > 0 {
				out[factories[st]] = req
			}
		}
	}
	return out, nil
}

// listFiles parses the non-test Go files of dir that can hold list resources or constants.
func listFiles(dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSource, err)
	}
	fset := token.NewFileSet()
	var out []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrSource, readErr)
		}
		if !bytes.Contains(src, []byte("ListResource")) && !bytes.Contains(src, []byte("const")) {
			continue
		}
		f, parseErr := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: parse %s: %w", ErrSource, name, parseErr)
		}
		out = append(out, f)
	}
	return out, nil
}

// collectDecl records a declaration's string constants, list factory or list config schema.
func collectDecl(decl ast.Decl, local, factories map[string]string, schemas map[string][]*ast.BlockStmt) {
	if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.CONST {
		for _, spec := range gd.Specs {
			addLocalConsts(local, spec)
		}
		return
	}
	fd, ok := decl.(*ast.FuncDecl)
	switch {
	case !ok || fd.Body == nil:
	case fd.Recv == nil:
		if tf, st := listFactory(fd); tf != "" && st != "" {
			factories[st] = tf
		}
	case fd.Name.Name == "ListResourceConfigSchema":
		schemas[receiver(fd)] = append(schemas[receiver(fd)], fd.Body)
	}
}

// addLocalConsts adds a service package's string constants, such as attrRestAPIID, by bare name.
func addLocalConsts(out map[string]string, spec ast.Spec) {
	vs, ok := spec.(*ast.ValueSpec)
	if !ok {
		return
	}
	for i, name := range vs.Names {
		if i < len(vs.Values) {
			if v, isString := stringLit(vs.Values[i]); isString {
				out[name.Name] = v
			}
		}
	}
}

// listFactory returns the TF type in fd's list annotation and the struct type fd returns.
func listFactory(fd *ast.FuncDecl) (string, string) {
	if fd.Doc == nil {
		return "", ""
	}
	m := listAnnotation.FindStringSubmatch(fd.Doc.Text())
	if m == nil {
		return "", ""
	}
	var st string
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.CompositeLit); ok && st == "" {
			if id, isIdent := lit.Type.(*ast.Ident); isIdent {
				st = id.Name
			}
		}
		return st == ""
	})
	return m[1], st
}

func receiver(fd *ast.FuncDecl) string {
	if len(fd.Recv.List) == 0 {
		return ""
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// requiredAttrs reads the top-level Attributes map of a list config schema and returns the
// names whose attribute sets Required: true, sorted.
func requiredAttrs(body *ast.BlockStmt, attrs map[string]string) []string {
	var out []string
	done := false // only the first Attributes map: nested blocks have their own
	ast.Inspect(body, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if done || !ok {
			return !done
		}
		if key, isIdent := kv.Key.(*ast.Ident); !isIdent || key.Name != "Attributes" {
			return true
		}
		m, isLit := kv.Value.(*ast.CompositeLit)
		done = true
		if !isLit {
			return false
		}
		for _, elt := range m.Elts {
			attr, isKV := elt.(*ast.KeyValueExpr)
			if !isKV {
				continue
			}
			if v, isAttr := attr.Value.(*ast.CompositeLit); isAttr && required(v) {
				out = append(out, resolveName(attr.Key, attrs))
			}
		}
		return false
	})
	slices.Sort(out)
	return slices.Compact(out)
}

func required(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, isIdent := kv.Key.(*ast.Ident)
		val, isVal := kv.Value.(*ast.Ident)
		if isIdent && isVal && key.Name == "Required" && val.Name == "true" {
			return true
		}
	}
	return false
}
