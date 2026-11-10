package tfstate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// LockFileName is the dependency lock file terraform init and tofu init write.
const LockFileName = ".terraform.lock.hcl"

// ReadLockFile returns the provider versions selected in dir's lock file, keyed by normalized
// source (see [Provider]). A missing file returns an empty map.
func ReadLockFile(dir string) (map[string]string, error) {
	path := filepath.Join(dir, LockFileName)
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock file: %w", err)
	}
	f, diags := hclparse.NewParser().ParseHCL(src, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse lock file: %w", diags)
	}
	content, _, diags := f.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "provider", LabelNames: []string{"source"}}},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse lock file: %w", diags)
	}
	out := map[string]string{}
	for _, b := range content.Blocks {
		attrs, _, attrDiags := b.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "version"}},
		})
		if attrDiags.HasErrors() {
			continue
		}
		a, ok := attrs.Attributes["version"]
		if !ok {
			continue
		}
		v, valDiags := a.Expr.Value(nil)
		if valDiags.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			continue
		}
		source, _ := normalizeSource(b.Labels[0])
		out[source] = v.AsString()
	}
	return out, nil
}
