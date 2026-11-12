package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/sassdavid/tfcensus/internal/codify"
)

// ErrNative is returned for a move the native strategy can't express.
var ErrNative = errors.New("not possible with the native strategy")

// NativeFileName is the file a native block writes into each unit it touches.
func NativeFileName(block string) string { return "moves_tfcensus_" + block + ".tf" }

func rawTokens(s string) hclwrite.Tokens {
	return hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(s)}}
}

// importFile writes one import block per moved instance: by identity when the state has one,
// else by the id attribute.
func importFile(header string, rs []*Resource) ([]byte, []string, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(hclwrite.Tokens{{Type: hclsyntax.TokenComment, Bytes: []byte(header + "\n")}})
	var notes []string
	for _, r := range rs {
		for i := range r.Instances {
			if deposed(r.Instances[i]) {
				continue
			}
			a, err := r.instanceAddr(i)
			if err != nil {
				return nil, nil, err
			}
			body.AppendNewline()
			blk := body.AppendNewBlock("import", nil).Body()
			blk.SetAttributeRaw("to", rawTokens(a.String()))
			if id := r.Instances[i]["identity"]; len(id) > 0 && string(id) != jsonNull {
				v, idErr := identityValue(id)
				if idErr != nil {
					return nil, nil, fmt.Errorf("identity of %s: %w", a.String(), idErr)
				}
				blk.SetAttributeValue("identity", v)
				continue
			}
			id := stringAttr(r.Instances[i], "id")
			if id == "" {
				return nil, nil, fmt.Errorf("%w: %s has neither an identity nor an id", ErrNative, a.String())
			}
			blk.SetAttributeValue("id", cty.StringVal(id))
			notes = append(notes, a.String()+" imports by id; check that the type's import ID is its id attribute")
		}
	}
	return f.Bytes(), notes, nil
}

func identityValue(raw json.RawMessage) (cty.Value, error) {
	t, err := ctyjson.ImpliedType(raw)
	if err != nil {
		return cty.NilVal, fmt.Errorf("decode: %w", err)
	}
	v, err := ctyjson.Unmarshal(raw, t)
	if err != nil {
		return cty.NilVal, fmt.Errorf("decode: %w", err)
	}
	return v, nil
}

// removedFile writes one removed block per moved resource, keeping the real objects. A removed
// block can't name single instances or module instances, so every instance of a resource must move.
func removedFile(header string, rs []*Resource, src *State) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(hclwrite.Tokens{{Type: hclsyntax.TokenComment, Bytes: []byte(header + "\n")}})
	var done []string
	for _, r := range rs {
		a, err := r.addr()
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(a.module, func(m moduleStep) bool { return m.key != nil }) {
			return nil, fmt.Errorf("%w: %s is in a module instance with a key; use the surgery strategy", ErrNative,
				a.resourceString())
		}
		if left := src.find(&a); left != nil {
			return nil, fmt.Errorf("%w: only some instances of %s move; a removed block takes the whole resource",
				ErrNative, a.resourceString())
		}
		if slices.Contains(done, a.resourceString()) {
			continue
		}
		done = append(done, a.resourceString())
		body.AppendNewline()
		blk := body.AppendNewBlock("removed", nil).Body()
		blk.SetAttributeRaw("from", rawTokens(a.resourceString()))
		blk.AppendNewBlock("lifecycle", nil).Body().SetAttributeValue("destroy", cty.False)
	}
	return f.Bytes(), nil
}

// writeNative writes the generated file into a unit directory, refusing to overwrite one unless
// force is set.
func writeNative(dir, block string, content []byte, force bool) error {
	return codify.Write(dir, []codify.File{{Name: NativeFileName(block), Content: content}}, force)
}
