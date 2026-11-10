package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

const (
	blockTerraform = "terraform"
	backendCloud   = "cloud"
)

// errNotLiteral marks a backend attribute that isn't a literal value.
var errNotLiteral = errors.New("not a literal value")

// plainRoots finds plain Terraform/OpenTofu root modules: directories whose .tf files have a
// backend block or a cloud block. It parses the files with hcl/v2 and runs nothing.
func plainRoots(repo, abs string) ([]Unit, error) {
	dirs := map[string][]string{}
	err := filepath.WalkDir(abs, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if path != abs && (skipDir(e.Name()) || strings.HasPrefix(e.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(e.Name(), ".tf") || strings.HasSuffix(e.Name(), ".tf.json") {
			dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", abs, err)
	}
	rel := relativizer(abs)
	var units []Unit
	for _, dir := range sortedKeys(dirs) {
		u, ok := plainRoot(dirs[dir], rel)
		if !ok {
			continue
		}
		p, relErr := filepath.Rel(abs, dir)
		if relErr != nil {
			return nil, fmt.Errorf("relativize %s: %w", dir, relErr)
		}
		u.Repo, u.Path = repo, filepath.ToSlash(p)
		units = append(units, u)
	}
	return units, nil
}

// plainRoot reads one module directory. It reports false when the module has no backend or cloud
// block, which makes it a child module, not a root.
func plainRoot(files []string, rel func(string) string) (Unit, bool) {
	u := Unit{Kind: KindTerraform, Warnings: []string{}}
	bodies := parseModule(files, func(msg string) { u.warn(rel(msg)) })
	backends, defaults := scanModule(bodies)
	if len(backends) == 0 {
		return u, false
	}
	if len(backends) > 1 {
		u.warn("more than one backend or cloud block; using the first")
	}
	bb := backends[0]
	b, warnings, err := backendFrom(bb.typ, bb.config, bb.configErr)
	if err != nil {
		u.unresolved(rel(err.Error()))
	} else {
		u.setBackend(b, MethodStatic)
		for _, w := range warnings {
			u.warn(rel(w))
		}
	}
	ctx := &hcl.EvalContext{Variables: map[string]cty.Value{"var": cty.ObjectVal(defaults)}}
	var p providerTarget
	for _, body := range bodies {
		if p = providerFromBody(body, ctx); p.account.value != "" || p.region.value != "" {
			break
		}
	}
	u.Target = resolveTarget(p, nil, b)
	return u, true
}

func parseModule(files []string, warn func(string)) []hcl.Body {
	parser := hclparse.NewParser()
	var bodies []hcl.Body
	for _, f := range slices.Sorted(slices.Values(files)) {
		parse := parser.ParseHCLFile
		if strings.HasSuffix(f, ".json") {
			parse = parser.ParseJSONFile
		}
		file, diags := parse(f)
		if diags.HasErrors() {
			warn("skipped unparsable file: " + diagError(diags).Error())
			continue
		}
		bodies = append(bodies, file.Body)
	}
	return bodies
}

// scanModule returns a module's backend and cloud blocks and its literal variable defaults.
func scanModule(bodies []hcl.Body) ([]backendBlock, map[string]cty.Value) {
	var backends []backendBlock
	defaults := map[string]cty.Value{}
	for _, body := range bodies {
		content, _, _ := body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
			{Type: blockTerraform}, {Type: "variable", LabelNames: []string{"name"}},
		}})
		for _, blk := range content.Blocks {
			if blk.Type == blockTerraform {
				backends = append(backends, backendBlocks(blk.Body)...)
				continue
			}
			attrs, _ := blk.Body.JustAttributes()
			if a, ok := attrs["default"]; ok {
				if v, diags := a.Expr.Value(nil); !diags.HasErrors() {
					defaults[blk.Labels[0]] = v
				}
			}
		}
	}
	return backends, defaults
}

type backendBlock struct {
	typ       string
	config    map[string]any
	configErr map[string]error
}

func backendBlocks(terraform hcl.Body) []backendBlock {
	tc, _, _ := terraform.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
		{Type: "backend", LabelNames: []string{"type"}}, {Type: backendCloud},
	}})
	var out []backendBlock
	for _, blk := range tc.Blocks {
		if blk.Type == backendCloud {
			out = append(out, backendBlock{typ: backendCloud})
			continue
		}
		bb := backendBlock{typ: blk.Labels[0], config: map[string]any{}, configErr: map[string]error{}}
		attrs, _ := blk.Body.JustAttributes()
		for name, a := range attrs {
			v, diags := a.Expr.Value(nil)
			if diags.HasErrors() {
				bb.configErr[name] = errNotLiteral
				continue
			}
			g, err := toGo(v)
			if err != nil {
				bb.configErr[name] = err
				continue
			}
			bb.config[name] = g
		}
		out = append(out, bb)
	}
	return out
}
