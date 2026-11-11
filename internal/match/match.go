package match

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Category is what matching concluded about a live resource.
type Category string

// Categories. Unmatched resources go on to the ownership rules.
const (
	CategoryManaged       Category = "managed"
	CategoryDoubleManaged Category = "double-managed"
	CategoryDerived       Category = "derived"
	CategoryUnmatched     Category = "unmatched"
	CategoryUndetermined  Category = "undetermined"
)

// KeyRule is the key kind of a security group rule matched by its tuple.
const KeyRule tfstate.KeyKind = "rule"

// Rule names, recorded on every result.
const (
	RuleIdentity     = "match/identity"
	RuleARN          = "match/arn"
	RuleID           = "match/id"
	RuleTuple        = "match/rule-tuple"
	RuleInline       = "match/inline-rule"
	RuleParent       = "match/parent-in-state"
	RuleNoMapping    = "match/no-type-mapping"
	RuleNotInState   = "match/not-in-state"
	RuleDoubleManage = "match/double-managed"
)

// Result is the match for one live resource.
type Result struct {
	Resource  inventory.CloudResource `json:"resource"`
	Category  Category                `json:"category"`
	Key       tfstate.KeyKind         `json:"key,omitempty"` // how it matched
	ManagedBy []tfstate.Ref           `json:"managed_by,omitempty"`
	Reason    string                  `json:"reason"`
	Rule      string                  `json:"rule"`
}

type key struct {
	kind  tfstate.KeyKind
	tf    string
	value string
}

// Matcher joins live resources with the state index.
type Matcher struct {
	m     *typemap.Map
	index map[key][]tfstate.Ref
	// inline holds the rule keys of aws_security_group inline rules, which only match when no
	// aws_security_group_rule does.
	inline map[string][]tfstate.Ref
}

// New indexes the state entries for matching.
func New(m *typemap.Map, ix *tfstate.Index) *Matcher {
	mt := &Matcher{m: m, index: map[key][]tfstate.Ref{}, inline: map[string][]tfstate.Ref{}}
	for i := range ix.Entries {
		e := &ix.Entries[i]
		ref := tfstate.Ref{Unit: e.Unit, Workspace: e.Workspace, Address: e.Address}
		for _, k := range mt.stateKeys(e) {
			mt.index[k] = appendRef(mt.index[k], ref)
		}
		for _, rk := range e.RuleKeys {
			v := e.Account + "/" + e.Region + "/" + rk
			if e.Type == "aws_security_group" {
				mt.inline[v] = appendRef(mt.inline[v], ref)
			} else {
				k := key{KeyRule, e.Type, v}
				mt.index[k] = appendRef(mt.index[k], ref)
			}
		}
	}
	return mt
}

func appendRef(refs []tfstate.Ref, r tfstate.Ref) []tfstate.Ref {
	if slices.Contains(refs, r) {
		return refs
	}
	return append(refs, r)
}

// IdentityParseAttrs returns, for tfstate's parser, the identity attributes of a type mapped to
// the resource attributes they come from. Account and region come from the entry instead.
func IdentityParseAttrs(m *typemap.Map) func(string) map[string]string {
	return func(tf string) map[string]string {
		t, ok := m.Type(tf)
		if !ok || !t.Curated {
			return nil
		}
		out := map[string]string{}
		for _, a := range t.IdentityAttrs {
			if a == typemap.IdentityAccountID || a == typemap.IdentityRegion {
				continue
			}
			out[a] = cmp.Or(t.IdentityFrom[a], a)
		}
		return out
	}
}

// canonical renders an identity as sorted key=value pairs without empty values, the form both
// sides of a match share.
func canonical(id map[string]string) string {
	parts := make([]string, 0, len(id))
	for _, k := range slices.Sorted(maps.Keys(id)) {
		if id[k] != "" {
			parts = append(parts, k+"="+id[k])
		}
	}
	return strings.Join(parts, ",")
}

func (mt *Matcher) stateKeys(e *tfstate.Entry) []key {
	t, ok := mt.m.Type(e.Type)
	region := e.Region
	if ok && t.Global {
		region = ""
	}
	var keys []key
	if ok {
		if id, found := stateIdentity(t, e, region); found {
			keys = append(keys, key{tfstate.KeyIdentity, e.Type, id})
		}
	}
	if e.ARN != "" {
		keys = append(keys, key{tfstate.KeyARN, e.Type, typemap.NormalizeARN(e.ARN)})
	}
	account := cmp.Or(e.Identity[typemap.IdentityAccountID], e.Account)
	if e.ID != "" && account != "" {
		keys = append(keys, key{tfstate.KeyID, e.Type, account + "/" + region + "/" + e.ID})
	}
	return keys
}

// stateIdentity returns the canonical identity of an entry: its identity block, else the identity
// attributes read from its resource attributes, completed with the entry's account and region.
func stateIdentity(t *typemap.Type, e *tfstate.Entry, region string) (string, bool) {
	id := e.Identity
	if len(id) == 0 {
		id = e.IdentityAttrs
	}
	if len(t.IdentityAttrs) == 0 || len(id) == 0 {
		return "", false
	}
	full := map[string]string{}
	for _, a := range t.IdentityAttrs {
		full[a] = id[a]
	}
	if full[typemap.IdentityAccountID] == "" && slices.Contains(t.IdentityAttrs, typemap.IdentityAccountID) {
		full[typemap.IdentityAccountID] = e.Account
	}
	if full[typemap.IdentityRegion] == "" && slices.Contains(t.IdentityAttrs, typemap.IdentityRegion) {
		full[typemap.IdentityRegion] = region
	}
	// An S3 bucket's ARN has no account, so an old state's bucket has no account to build an
	// identity with; its ARN still matches.
	if !complete(t, full) {
		return "", false
	}
	return canonical(full), true
}

// complete reports whether every identity attribute that must have a value has one.
func complete(t *typemap.Type, id map[string]string) bool {
	for _, a := range t.IdentityAttrs {
		if id[a] == "" && !slices.Contains(t.IdentityOptional, a) {
			return false
		}
	}
	return true
}

// fields returns the lookup for templates on a live resource: curated live templates, the
// lister's attributes, the ARN pattern's captures, then the resource's own fields.
func fields(t *typemap.Type, r *inventory.CloudResource) func(string) string {
	var captures map[string]string
	if t.ARN != nil && r.ARN != "" {
		captures = t.ARN.Match(typemap.NormalizeARN(r.ARN))
	}
	base := func(name string) string {
		if v, ok := r.Attrs[name]; ok {
			return v
		}
		if v, ok := captures[name]; ok {
			return v
		}
		switch name {
		case typemap.FieldID:
			return r.ID
		case typemap.FieldARN:
			return r.ARN
		case typemap.FieldAccount:
			return r.Account
		case typemap.FieldRegion:
			return r.Region
		}
		return ""
	}
	return func(name string) string {
		if tmpl, ok := t.Live[name]; ok {
			v, _ := tmpl.Render(base)
			return v
		}
		return base(name)
	}
}

// liveKeys returns the keys of r as TF type t, strongest first.
func liveKeys(t *typemap.Type, r *inventory.CloudResource) []key {
	lookup := fields(t, r)
	region := r.Region
	if t.Global {
		region = ""
	}
	var keys []key
	if len(t.IdentityAttrs) > 0 {
		id := map[string]string{}
		for _, a := range t.IdentityAttrs {
			var v string
			switch a {
			case typemap.IdentityAccountID:
				v = r.Account
			case typemap.IdentityRegion:
				v = region
			default:
				v = lookup(a)
			}
			id[a] = v
		}
		if complete(t, id) {
			keys = append(keys, key{tfstate.KeyIdentity, t.TF, canonical(id)})
		}
	}
	if r.ARN != "" {
		keys = append(keys, key{tfstate.KeyARN, t.TF, typemap.NormalizeARN(r.ARN)})
	}
	for _, tmpl := range t.ID {
		if v, ok := tmpl.Render(lookup); ok && r.Account != "" {
			keys = append(keys, key{tfstate.KeyID, t.TF, r.Account + "/" + region + "/" + v})
		}
	}
	return keys
}

// Match categorizes every live resource. Results keep the order of resources.
func (mt *Matcher) Match(resources []inventory.CloudResource) []Result {
	out := make([]Result, len(resources))
	for i := range resources {
		out[i] = mt.one(&resources[i])
	}
	return out
}

func (mt *Matcher) one(r *inventory.CloudResource) Result {
	res := Result{Resource: *r}
	if r.CFNType == "" || !mt.m.Mapped(r.CFNType) {
		res.Category, res.Rule = CategoryUndetermined, RuleNoMapping
		res.Reason = "no type mapping for " + cmp.Or(r.CFNType, r.SourceType, "an unknown type")
		return res
	}
	if refs, kind := mt.lookup(r); len(refs) > 0 {
		return decide(res, refs, kind)
	}
	if parent, refs := mt.managedParent(r); len(refs) > 0 {
		res.Category, res.Rule, res.ManagedBy = CategoryDerived, RuleParent, refs
		res.Reason = "child of managed " + refs[0].Address + " (" + parent + ")"
		return res
	}
	res.Category, res.Rule, res.Reason = CategoryUnmatched, RuleNotInState, "no state manages it"
	return res
}

func (mt *Matcher) lookup(r *inventory.CloudResource) ([]tfstate.Ref, tfstate.KeyKind) {
	types := mt.m.ForCFN(r.CFNType)
	keys := make([][]key, len(types))
	for i, t := range types {
		keys[i] = liveKeys(t, r)
	}
	for _, kind := range []tfstate.KeyKind{tfstate.KeyIdentity, tfstate.KeyARN, tfstate.KeyID} {
		if refs := mt.refs(keys, kind); len(refs) > 0 {
			return refs, kind
		}
	}
	return mt.ruleRefs(r, types)
}

// refs returns the state refs of the keys of one kind.
func (mt *Matcher) refs(keys [][]key, kind tfstate.KeyKind) []tfstate.Ref {
	var refs []tfstate.Ref
	for _, ks := range keys {
		for _, k := range ks {
			if k.kind != kind {
				continue
			}
			for _, ref := range mt.index[k] {
				refs = appendRef(refs, ref)
			}
		}
	}
	return refs
}

// ruleRefs matches a security group rule by its tuple: rule resources first, inline blocks of
// aws_security_group only when no rule resource has the tuple.
func (mt *Matcher) ruleRefs(r *inventory.CloudResource, types []*typemap.Type) ([]tfstate.Ref, tfstate.KeyKind) {
	rk := r.Attrs["rule_key"]
	if rk == "" {
		return nil, ""
	}
	v := r.Account + "/" + r.Region + "/" + rk
	var refs []tfstate.Ref
	for _, t := range types {
		if t.Tuple {
			for _, ref := range mt.index[key{KeyRule, t.TF, v}] {
				refs = appendRef(refs, ref)
			}
		}
	}
	if len(refs) > 0 {
		return refs, KeyRule
	}
	return mt.inline[v], KeyRule
}

func decide(res Result, refs []tfstate.Ref, kind tfstate.KeyKind) Result {
	slices.SortFunc(refs, func(a, b tfstate.Ref) int {
		return cmp.Or(
			strings.Compare(a.Unit, b.Unit),
			strings.Compare(a.Workspace, b.Workspace),
			strings.Compare(a.Address, b.Address),
		)
	})
	res.Key, res.ManagedBy = kind, refs
	res.Rule = map[tfstate.KeyKind]string{
		tfstate.KeyIdentity: RuleIdentity, tfstate.KeyARN: RuleARN, tfstate.KeyID: RuleID, KeyRule: RuleTuple,
	}[kind]
	units := map[string]bool{}
	for _, ref := range refs {
		units[ref.Unit+"\x00"+ref.Workspace] = true
	}
	if len(units) > 1 {
		res.Category, res.Rule = CategoryDoubleManaged, RuleDoubleManage
		res.Reason = "managed by " + refs[0].Address + " and " + refs[1].Address + " in different states"
		return res
	}
	res.Category = CategoryManaged
	res.Reason = "matched " + refs[0].Unit + " " + refs[0].Address + " by " + string(kind)
	if kind == KeyRule && strings.HasPrefix(stateType(refs[0].Address), "aws_security_group.") {
		res.Rule = RuleInline
		res.Reason = "inline rule of " + refs[0].Unit + " " + refs[0].Address
	}
	return res
}

// stateType returns the resource part of an address, without its module path.
func stateType(address string) string {
	for {
		rest, ok := strings.CutPrefix(address, "module.")
		if !ok {
			return address
		}
		_, after, found := strings.Cut(rest, ".")
		if !found {
			return address
		}
		address = after
	}
}

// managedParent returns the parent's type and the state refs that manage it, when r's type is a
// derived child, r meets the type's conditions, and its parent is in state.
func (mt *Matcher) managedParent(r *inventory.CloudResource) (string, []tfstate.Ref) {
	for _, t := range mt.m.ForCFN(r.CFNType) {
		if t.Derived == nil || !meets(t.Derived.When, r.Attrs) {
			continue
		}
		for _, p := range r.Parents {
			if p.CFNType != t.Derived.Parent {
				continue
			}
			parent := inventory.CloudResource{
				Account: r.Account, Region: r.Region, CFNType: p.CFNType, ARN: p.ARN, ID: p.ID,
				Attrs: map[string]string{},
			}
			if refs, _ := mt.lookup(&parent); len(refs) > 0 {
				return p.CFNType, refs
			}
		}
	}
	return "", nil
}

func meets(when map[string][]string, attrs map[string]string) bool {
	for field, values := range when {
		if !slices.Contains(values, attrs[field]) {
			return false
		}
	}
	return true
}

// Summary counts results per category, and undetermined results per reason.
type Summary struct {
	Counts       map[Category]int `json:"counts"`
	Undetermined map[string]int   `json:"undetermined"`
}

// Summarize counts results.
func Summarize(results []Result) Summary {
	s := Summary{Counts: make(map[Category]int), Undetermined: map[string]int{}}
	for i := range results {
		s.Counts[results[i].Category]++
		if results[i].Category == CategoryUndetermined {
			s.Undetermined[results[i].Reason]++
		}
	}
	return s
}
