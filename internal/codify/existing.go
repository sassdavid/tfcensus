package codify

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Existing is what the target directory's .tf files already declare.
type Existing struct {
	names names
	keys  map[string]string // import key to the file that has it
}

// ReadExisting reads the resource names and the literal import ids and identities of the .tf
// files directly in dir, except the files named in skip. Expressions that need evaluation are
// left out.
func ReadExisting(dir string, skip ...string) (*Existing, error) {
	ex := &Existing{names: names{}, keys: map[string]string{}}
	paths, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return nil, fmt.Errorf("list .tf files: %w", err)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if slices.Contains(skip, filepath.Base(p)) {
			continue
		}
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", filepath.Base(p), readErr)
		}
		f, diags := hclsyntax.ParseConfig(src, filepath.Base(p), hcl.InitialPos)
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse %s: %w", filepath.Base(p), diags)
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, b := range body.Blocks {
			switch {
			case b.Type == "resource" && len(b.Labels) == 2:
				ex.names.take(b.Labels[0], b.Labels[1])
			case b.Type == "import":
				ex.addImport(b.Body, filepath.Base(p))
			}
		}
	}
	return ex, nil
}

func (ex *Existing) addImport(body *hclsyntax.Body, file string) {
	to, ok := body.Attributes["to"]
	if !ok {
		return
	}
	trav, diags := hcl.AbsTraversalForExpr(to.Expr)
	if diags.HasErrors() || len(trav) < 2 {
		return
	}
	// A module address has more steps; only the resource steps at the end name a resource here.
	root, rootOK := trav[0].(hcl.TraverseRoot)
	attr, attrOK := trav[1].(hcl.TraverseAttr)
	if !rootOK || !attrOK || len(trav) > 3 || root.Name == "module" {
		return
	}
	ex.names.take(root.Name, attr.Name)
	if a, found := body.Attributes["id"]; found {
		if v, d := a.Expr.Value(nil); !d.HasErrors() && v.Type() == cty.String && v.IsKnown() && !v.IsNull() {
			ex.keys[idKey(root.Name, v.AsString())] = file
		}
	}
	if a, found := body.Attributes["identity"]; found {
		v, d := a.Expr.Value(nil)
		if d.HasErrors() || !v.IsKnown() || v.IsNull() || !v.CanIterateElements() {
			return
		}
		id := map[string]string{}
		for it := v.ElementIterator(); it.Next(); {
			k, val := it.Element()
			if val.Type() == cty.String && val.IsKnown() && !val.IsNull() {
				id[k.AsString()] = val.AsString()
			}
		}
		ex.keys[identityKey(root.Name, id)] = file
	}
}

func idKey(tfType, id string) string { return "id\x00" + tfType + "\x00" + id }

// identityKey leaves out account_id and region, which hand-written import blocks often omit.
func identityKey(tfType string, id map[string]string) string {
	parts := make([]string, 0, len(id))
	for k, v := range id {
		if k != typemap.IdentityAccountID && k != typemap.IdentityRegion {
			parts = append(parts, k+"="+v)
		}
	}
	slices.Sort(parts)
	return "identity\x00" + tfType + "\x00" + strings.Join(parts, ",")
}
