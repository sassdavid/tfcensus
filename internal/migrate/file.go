package migrate

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"
)

// Strategies.
const (
	StrategySurgery = "surgery"
	StrategyNative  = "native"
)

const blockType = "units"

// ErrFile is returned for a migration file that doesn't parse or validate.
var ErrFile = errors.New("invalid migration file")

var blockNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// File is a parsed migration file.
type File struct {
	Name   string   // the base name, which history records use
	Blocks []*Block // in file order
}

// Block is one migration "units" block.
type Block struct {
	Name     string
	Strategy string
	From     string
	To       []Destination // in file order; empty for an in-place block
	Actions  []string      // tfmigrate actions run on From; empty for a move between units
	Line     int

	actions []action
}

// Destination is one entry of a block's to map.
type Destination struct {
	Unit      string
	Addresses []string

	sels []selector
}

// Units returns every unit the block writes: the destinations, then From.
func (b *Block) Units() []string {
	out := make([]string, 0, len(b.To)+1)
	for _, d := range b.To {
		out = append(out, d.Unit)
	}
	return append(out, b.From)
}

// ConfirmPath returns what the person types to confirm the block: its last destination, or From
// for an in-place block.
func (b *Block) ConfirmPath() string {
	if len(b.To) == 0 {
		return b.From
	}
	return b.To[len(b.To)-1].Unit
}

// ParseFile reads and validates a migration file. env and vars fill env.NAME and var.NAME.
func ParseFile(file string, env, vars map[string]string) (*File, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read migration file: %w", err)
	}
	return Parse(src, filepath.Base(file), env, vars)
}

// Parse validates a migration file's content; name is its base name.
func Parse(src []byte, name string, env, vars map[string]string) (*File, error) {
	f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%w: %w", ErrFile, diags)
	}
	content, diags := f.Body.Content(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "migration", LabelNames: []string{"type", "name"}}},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("%w: %w", ErrFile, diags)
	}
	ctx := &hcl.EvalContext{Variables: map[string]cty.Value{"env": stringMap(env), "var": stringMap(vars)}}
	out := &File{Name: name}
	seen := map[string]bool{}
	for _, hb := range content.Blocks {
		b, err := parseBlock(hb, ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: %s:%d: %w", ErrFile, name, hb.DefRange.Start.Line, err)
		}
		if seen[b.Name] {
			return nil, fmt.Errorf("%w: %s:%d: block %q is defined twice", ErrFile, name, b.Line, b.Name)
		}
		seen[b.Name] = true
		out.Blocks = append(out.Blocks, b)
	}
	if len(out.Blocks) == 0 {
		return nil, fmt.Errorf("%w: %s has no migration \"units\" blocks", ErrFile, name)
	}
	return out, nil
}

func stringMap(m map[string]string) cty.Value {
	vals := make(map[string]cty.Value, len(m))
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.ObjectVal(vals)
}

func parseBlock(hb *hcl.Block, ctx *hcl.EvalContext) (*Block, error) {
	if hb.Labels[0] != blockType {
		return nil, fmt.Errorf("migration type %q is not supported; tfcensus reads migration \"units\" blocks "+
			"(see docs/runbooks/move.md for translating tfmigrate files)", hb.Labels[0])
	}
	b := &Block{Name: hb.Labels[1], Line: hb.DefRange.Start.Line, Strategy: StrategySurgery}
	if !blockNameRe.MatchString(b.Name) {
		return nil, fmt.Errorf("block name %q: use letters, digits, _ and -, starting with a letter or _", b.Name)
	}
	body, diags := hb.Body.Content(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{
		{Name: "from", Required: true}, {Name: "to"}, {Name: "actions"}, {Name: "strategy"},
	}})
	if diags.HasErrors() {
		return nil, diags
	}
	var err error
	if err = evalInto(body.Attributes["from"], ctx, &b.From); err != nil {
		return nil, err
	}
	if b.From, err = cleanUnit(b.From); err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	if a, ok := body.Attributes["strategy"]; ok {
		if err = evalInto(a, ctx, &b.Strategy); err != nil {
			return nil, err
		}
	}
	if b.Strategy != StrategySurgery && b.Strategy != StrategyNative {
		return nil, fmt.Errorf("strategy %q: want surgery or native", b.Strategy)
	}
	toAttr, hasTo := body.Attributes["to"]
	actAttr, hasActions := body.Attributes["actions"]
	switch {
	case hasTo == hasActions:
		return nil, errors.New("set exactly one of to (a move between units) and actions (a change inside from)")
	case hasTo:
		err = b.parseTo(toAttr, ctx)
	default:
		err = b.parseActions(actAttr, ctx)
	}
	return b, err
}

func (b *Block) parseTo(attr *hcl.Attribute, ctx *hcl.EvalContext) error {
	obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return errors.New(`to: want a map of unit path to addresses, such as { "live/a" = ["aws_s3_bucket.x"] }`)
	}
	if len(obj.Items) == 0 {
		return errors.New("to: name at least one destination unit")
	}
	seen := map[string]bool{b.From: true}
	for _, item := range obj.Items {
		d, err := parseDestination(item, ctx)
		if err != nil {
			return err
		}
		if seen[d.Unit] {
			return fmt.Errorf("to: %s is the source or listed twice", d.Unit)
		}
		seen[d.Unit] = true
		b.To = append(b.To, d)
	}
	return nil
}

func parseDestination(item hclsyntax.ObjectConsItem, ctx *hcl.EvalContext) (Destination, error) {
	var d Destination
	kv, diags := item.KeyExpr.Value(ctx)
	if diags.HasErrors() {
		return d, diags
	}
	if kv.Type() != cty.String || kv.IsNull() {
		return d, errors.New("to: unit paths must be strings")
	}
	var err error
	if d.Unit, err = cleanUnit(kv.AsString()); err != nil {
		return d, fmt.Errorf("to: %w", err)
	}
	vv, diags := item.ValueExpr.Value(ctx)
	if diags.HasErrors() {
		return d, diags
	}
	if d.Addresses, err = toStrings(vv); err != nil || len(d.Addresses) == 0 {
		return d, fmt.Errorf("to[%q]: want a non-empty list of addresses", d.Unit)
	}
	for _, a := range d.Addresses {
		sel, selErr := newSelector(a)
		if selErr != nil {
			return d, fmt.Errorf("to[%q]: %w", d.Unit, selErr)
		}
		d.sels = append(d.sels, sel)
	}
	return d, nil
}

func (b *Block) parseActions(attr *hcl.Attribute, ctx *hcl.EvalContext) error {
	if b.Strategy == StrategyNative {
		return errors.New("actions: the native strategy moves between units; use to")
	}
	v, diags := attr.Expr.Value(ctx)
	if diags.HasErrors() {
		return diags
	}
	var err error
	if b.Actions, err = toStrings(v); err != nil {
		return fmt.Errorf("actions: %w", err)
	}
	if len(b.Actions) == 0 {
		return errors.New("actions: list at least one action")
	}
	for _, s := range b.Actions {
		a, actErr := parseAction(s)
		if actErr != nil {
			return actErr
		}
		b.actions = append(b.actions, a)
	}
	return nil
}

// toStrings converts a list or tuple of strings, which is what an HCL list literal is.
func toStrings(v cty.Value) ([]string, error) {
	lv, err := convert.Convert(v, cty.List(cty.String))
	if err != nil {
		return nil, fmt.Errorf("want a list of strings: %w", err)
	}
	var out []string
	if err = gocty.FromCtyValue(lv, &out); err != nil {
		return nil, fmt.Errorf("want a list of strings: %w", err)
	}
	return out, nil
}

func evalInto(attr *hcl.Attribute, ctx *hcl.EvalContext, dst any) error {
	v, diags := attr.Expr.Value(ctx)
	if diags.HasErrors() {
		return diags
	}
	if err := gocty.FromCtyValue(v, dst); err != nil {
		return fmt.Errorf("%s: %w", attr.Name, err)
	}
	return nil
}

// cleanUnit checks that p is a relative unit path inside the repository and returns it with
// forward slashes and no trailing slash.
func cleanUnit(p string) (string, error) {
	s := strings.ReplaceAll(p, `\`, "/")
	if s == "" || strings.HasPrefix(s, "/") || filepath.IsAbs(p) || (len(s) > 1 && s[1] == ':') {
		return "", fmt.Errorf("unit path %q must be relative to the repository root", p)
	}
	c := path.Clean(s)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("unit path %q must stay inside the repository", p)
	}
	return c, nil
}
