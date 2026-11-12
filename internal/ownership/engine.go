package ownership

import (
	"embed"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
	"go.yaml.in/yaml/v4"
)

//go:embed packs/*.yaml
var packFS embed.FS

// Category is what a rule says about the resources it matches.
type Category string

// Rule categories.
const (
	CategoryOtherIaC   Category = "other-iac"
	CategoryAWSManaged Category = "aws-managed"
	CategoryDerived    Category = "derived"
	CategoryIgnored    Category = "ignored"
)

// Categories returns the categories a rule can set.
func Categories() []Category {
	return []Category{CategoryOtherIaC, CategoryAWSManaged, CategoryDerived, CategoryIgnored}
}

// Pack names, in evaluation order.
const (
	PackAWSDefaults = "aws-defaults"
	PackAWSService  = "aws-service"
	PackOtherIaC    = "other-iac"
	PackDerived     = "derived"
	PackEphemeral   = "ephemeral"
)

// PackOrder returns the built-in packs in the order they run.
func PackOrder() []string {
	return []string{PackAWSDefaults, PackAWSService, PackOtherIaC, PackDerived, PackEphemeral}
}

func packCategory(pack string) Category {
	switch pack {
	case PackAWSDefaults, PackAWSService:
		return CategoryAWSManaged
	case PackOtherIaC:
		return CategoryOtherIaC
	case PackDerived:
		return CategoryDerived
	}
	return CategoryIgnored
}

// Rule is one ownership rule as written: a built-in pack entry or a user rule.
type Rule struct {
	Name     string   `yaml:"name"`
	Category Category `yaml:"category"`
	Owner    string   `yaml:"owner"`
	Match    string   `yaml:"match"`
	Reason   string   `yaml:"reason"`
	Expires  string   `yaml:"expires"` // YYYY-MM-DD, the last day the rule applies (UTC)
}

// Resource is what a rule sees as `resource`. Field names are the CEL names.
type Resource struct {
	Account       string            `cel:"account"       json:"account"`
	Region        string            `cel:"region"        json:"region,omitempty"`
	Type          string            `cel:"type"          json:"type,omitempty"`        // CloudFormation type
	SourceType    string            `cel:"source_type"   json:"source_type,omitempty"` // the layer's type, when the CloudFormation type is unknown
	TFTypes       []string          `cel:"tf_types"      json:"tf_types,omitempty"`
	ARN           string            `cel:"arn"           json:"arn,omitempty"`
	ID            string            `cel:"id"            json:"id,omitempty"`
	Name          string            `cel:"name"          json:"name,omitempty"`
	Tags          map[string]string `cel:"tags"          json:"tags,omitempty"`
	Attrs         map[string]string `cel:"attrs"         json:"attrs,omitempty"`
	Layer         string            `cel:"layer"         json:"layer,omitempty"` // the first layer that reported it
	Sources       []string          `cel:"sources"       json:"sources,omitempty"`
	Parents       []Related         `cel:"parents"       json:"parents,omitempty"`
	Relationships []Related         `cel:"relationships" json:"relationships,omitempty"`
	Created       string            `cel:"created"       json:"created,omitempty"` // RFC 3339
}

// Related is a parent or another related resource, with what the inventory knows about it.
type Related struct {
	Type         string            `cel:"type"         json:"type"`
	ID           string            `cel:"id"           json:"id,omitempty"`
	ARN          string            `cel:"arn"          json:"arn,omitempty"`
	Name         string            `cel:"name"         json:"name,omitempty"`
	Relationship string            `cel:"relationship" json:"relationship,omitempty"`
	Tags         map[string]string `cel:"tags"         json:"tags,omitempty"`
	Attrs        map[string]string `cel:"attrs"        json:"attrs,omitempty"`
	Found        bool              `cel:"found"        json:"found,omitempty"` // the inventory has it
}

// Unit is what a rule sees as `unit`: the unit that manages the resource, empty when none does.
type Unit struct {
	Repo      string `cel:"repo"      json:"repo,omitempty"`
	Path      string `cel:"path"      json:"path,omitempty"`
	Workspace string `cel:"workspace" json:"workspace,omitempty"`
	Address   string `cel:"address"   json:"address,omitempty"`
}

// Options configures an [Engine].
type Options struct {
	Packs     map[string]bool // pack name to on or off; a missing pack is on
	Rules     []Rule          // user rules, in order
	NeverHide []Category
	Now       func() time.Time
	// InState reports whether a state manages a resource of a TF type with an ID; it backs the
	// in_state function. Nil means nothing is in state.
	InState func(tfType, id string) bool
}

type compiled struct {
	Rule

	id      string // pack/<pack>/<name> or user/<name>
	pack    string // "" for user rules
	expires time.Time
	prg     cel.Program
}

// Engine evaluates rules. It is safe for concurrent use.
type Engine struct {
	opts   Options
	rules  []*compiled
	guards []*compiled
}

// ErrRule is wrapped by every rule error.
var ErrRule = errors.New("invalid ownership rule")

// RuleError is a rule that doesn't compile or doesn't fit the schema.
type RuleError struct {
	Rule   string
	Column int // 1-based, 0 when the problem isn't in the expression
	Msg    string
}

func (e *RuleError) Error() string {
	if e.Column > 0 {
		return fmt.Sprintf("rule %q: column %d: %s", e.Rule, e.Column, e.Msg)
	}
	return fmt.Sprintf("rule %q: %s", e.Rule, e.Msg)
}

func (*RuleError) Unwrap() error { return ErrRule }

// New compiles the built-in packs and the user rules. It returns every rule error at once.
func New(opts Options) (*Engine, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	env, err := newEnv(opts.InState)
	if err != nil {
		return nil, err
	}
	e := &Engine{opts: opts}
	if err = e.compileBuiltins(env); err != nil {
		return nil, err
	}
	var errs []error
	seen := map[string]bool{}
	for _, r := range opts.Rules {
		if seen[r.Name] {
			errs = append(errs, &RuleError{Rule: r.Name, Msg: "name is used by an earlier rule"})
			continue
		}
		seen[r.Name] = true
		c, cerr := compile(env, r, "user/"+r.Name, "")
		if cerr != nil {
			errs = append(errs, cerr)
			continue
		}
		e.rules = append(e.rules, c)
	}
	if err = errors.Join(errs...); err != nil {
		return nil, err
	}
	return e, nil
}

// compileBuiltins compiles the packs and the guards. Their errors are bugs, so the first one is
// returned.
func (e *Engine) compileBuiltins(env *cel.Env) error {
	for _, pack := range PackOrder() {
		rules, err := packRules(pack)
		if err != nil {
			return err
		}
		for _, r := range rules {
			r.Category = packCategory(pack)
			if r.Owner == "" && r.Category != CategoryIgnored && r.Category != CategoryOtherIaC {
				r.Owner = "aws"
			}
			c, cerr := compile(env, r, "pack/"+pack+"/"+r.Name, pack)
			if cerr != nil {
				return fmt.Errorf("built-in pack %s: %w", pack, cerr)
			}
			e.rules = append(e.rules, c)
		}
	}
	guards, err := packRules("guards")
	if err != nil {
		return err
	}
	for _, g := range guards {
		c, cerr := compile(env, g, "guard/"+g.Name, "guards")
		if cerr != nil {
			return fmt.Errorf("built-in guards: %w", cerr)
		}
		e.guards = append(e.guards, c)
	}
	return nil
}

func packRules(name string) ([]Rule, error) {
	b, err := packFS.ReadFile("packs/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("read pack %s: %w", name, err)
	}
	var rules []Rule
	if err = yaml.Unmarshal(b, &rules); err != nil {
		return nil, fmt.Errorf("decode pack %s: %w", name, err)
	}
	return rules, nil
}

func newEnv(inState func(string, string) bool) (*cel.Env, error) {
	env, err := cel.NewEnv(
		ext.NativeTypes(reflect.TypeFor[Resource](), reflect.TypeFor[Related](), reflect.TypeFor[Unit](),
			ext.ParseStructTags(true)),
		cel.Variable("resource", cel.ObjectType("ownership.Resource")),
		cel.Variable("unit", cel.ObjectType("ownership.Unit")),
		cel.OptionalTypes(),
		ext.Strings(),
		cel.Function("in_state",
			cel.Overload("in_state_string_string", []*cel.Type{cel.StringType, cel.StringType}, cel.BoolType,
				cel.BinaryBinding(func(tf, id ref.Val) ref.Val {
					t, ok1 := tf.(types.String)
					i, ok2 := id.(types.String)
					if !ok1 || !ok2 || inState == nil {
						return types.False
					}
					return types.Bool(inState(string(t), string(i)))
				}))),
	)
	if err != nil {
		return nil, fmt.Errorf("create CEL environment: %w", err)
	}
	return env, nil
}

func compile(env *cel.Env, r Rule, id, pack string) (*compiled, error) {
	c := &compiled{Rule: r, id: id, pack: pack}
	switch {
	case r.Name == "":
		return nil, &RuleError{Rule: id, Msg: "name is empty"}
	case strings.TrimSpace(r.Match) == "":
		return nil, &RuleError{Rule: r.Name, Msg: "match is empty"}
	case strings.TrimSpace(r.Reason) == "":
		return nil, &RuleError{Rule: r.Name, Msg: "reason is empty"}
	case pack != "guards" && !slices.Contains(Categories(), r.Category):
		return nil, &RuleError{
			Rule: r.Name,
			Msg:  fmt.Sprintf("category %q: want one of %s", r.Category, categoryList()),
		}
	}
	if r.Expires != "" {
		t, err := ParseExpires(r.Expires)
		if err != nil {
			return nil, &RuleError{Rule: r.Name, Msg: fmt.Sprintf("expires %q: want YYYY-MM-DD", r.Expires)}
		}
		c.expires = t
	}
	ast, iss := env.Compile(r.Match)
	if iss != nil && iss.Err() != nil {
		first := iss.Errors()[0]
		return nil, &RuleError{Rule: r.Name, Column: first.Location.Column() + 1, Msg: first.Message}
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, &RuleError{Rule: r.Name, Msg: "match returns " + ast.OutputType().String() + ", want bool"}
	}
	prg, err := env.Program(ast, cel.EvalOptions(cel.OptOptimize))
	if err != nil {
		return nil, &RuleError{Rule: r.Name, Msg: err.Error()}
	}
	c.prg = prg
	return c, nil
}

func categoryList() string {
	out := make([]string, 0, len(Categories()))
	for _, c := range Categories() {
		out = append(out, string(c))
	}
	return strings.Join(out, ", ")
}

// Outcome is what one rule did for one resource.
type Outcome string

// Rule outcomes in a [Step].
const (
	OutcomeMatch     Outcome = "match"
	OutcomeNoMatch   Outcome = "no match"
	OutcomeError     Outcome = "error"      // the expression failed, which counts as no match
	OutcomeExpired   Outcome = "expired"    // past its expires date
	OutcomePackOff   Outcome = "pack off"   // its pack is turned off
	OutcomeNeverHide Outcome = "never hide" // its category is in never_hide and the resource is guarded
	OutcomeSkipped   Outcome = "skipped"    // an earlier rule matched
)

// Step is one rule's line in an evaluation trace.
type Step struct {
	Rule    string  `json:"rule"`
	Outcome Outcome `json:"outcome"`
	Detail  string  `json:"detail,omitempty"`
}

// Decision is the result of evaluating the rules for one resource. Matched is false when no rule
// matched; Category, Owner and Reason are then empty and Rule is [RuleNoMatch].
type Decision struct {
	Matched  bool     `json:"matched"`
	Category Category `json:"category,omitempty"`
	Owner    string   `json:"owner,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Rule     string   `json:"rule"`
	Trace    []Step   `json:"trace,omitempty"`
}

// ParseExpires parses an expires date (YYYY-MM-DD, UTC) and returns the first instant after that
// day, so a rule applies through its whole last day. Drift noise rules use the same rule.
func ParseExpires(date string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse expires: %w", err)
	}
	return t.AddDate(0, 0, 1), nil
}

// Expired reports whether a rule with the expires instant from [ParseExpires] no longer applies at
// now. The zero instant never expires.
func Expired(expires, now time.Time) bool { return !expires.IsZero() && !now.Before(expires) }

// RuleNoMatch is the rule of a [Decision] in which no rule matched.
const RuleNoMatch = "rules/no-match"

// Evaluate runs the rules in order for r and unit (nil when no unit manages r). The first rule
// that matches decides; with trace, every rule gets a [Step], else only the matching one.
func (e *Engine) Evaluate(r *Resource, unit *Unit, trace bool) Decision {
	if unit == nil {
		unit = &Unit{}
	}
	vars := map[string]any{"resource": r, "unit": unit}
	now := e.opts.Now()
	guarded := e.guarded(vars)
	d := Decision{Rule: RuleNoMatch}
	for _, c := range e.rules {
		if d.Matched {
			if trace {
				d.Trace = append(d.Trace, Step{Rule: c.id, Outcome: OutcomeSkipped})
			}
			continue
		}
		step := e.step(c, vars, now, guarded)
		if step.Outcome == OutcomeMatch {
			d = Decision{
				Matched:  true,
				Category: c.Category,
				Owner:    c.Owner,
				Reason:   c.Reason,
				Rule:     c.id,
				Trace:    d.Trace,
			}
		}
		if trace {
			d.Trace = append(d.Trace, step)
		}
	}
	return d
}

func (e *Engine) step(c *compiled, vars map[string]any, now time.Time, guarded string) Step {
	s := Step{Rule: c.id}
	switch {
	case c.pack != "" && !e.packOn(c.pack):
		s.Outcome = OutcomePackOff
	case Expired(c.expires, now):
		s.Outcome, s.Detail = OutcomeExpired, "last day "+c.Expires
	case c.pack == "" && guarded != "" && slices.Contains(e.opts.NeverHide, c.Category):
		s.Outcome, s.Detail = OutcomeNeverHide, string(c.Category)+" is in never_hide and "+guarded
	default:
		ok, err := eval(c, vars)
		switch {
		case err != nil:
			s.Outcome, s.Detail = OutcomeError, err.Error()
		case ok:
			s.Outcome = OutcomeMatch
		default:
			s.Outcome = OutcomeNoMatch
		}
	}
	return s
}

// guarded returns the reason of the first guard that matches, or "".
func (e *Engine) guarded(vars map[string]any) string {
	for _, g := range e.guards {
		if ok, err := eval(g, vars); err == nil && ok {
			return g.Reason
		}
	}
	return ""
}

func eval(c *compiled, vars map[string]any) (bool, error) {
	out, _, err := c.prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("evaluate: %w", err)
	}
	b, ok := out.Value().(bool)
	return ok && b, nil
}

func (e *Engine) packOn(pack string) bool {
	on, ok := e.opts.Packs[pack]
	return !ok || on
}

// Expired returns the names of user rules that are past their expires date, sorted, so the
// caller can warn about each once.
func (e *Engine) Expired() []string {
	now := e.opts.Now()
	var out []string
	for _, c := range e.rules {
		if c.pack == "" && Expired(c.expires, now) {
			out = append(out, c.Name)
		}
	}
	slices.Sort(out)
	return out
}

// Rules returns the IDs of every rule in evaluation order.
func (e *Engine) Rules() []string {
	out := make([]string, len(e.rules))
	for i, c := range e.rules {
		out[i] = c.id
	}
	return out
}
