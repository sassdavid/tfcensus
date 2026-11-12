package match

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Index builds the state index of states and fills its double-managed resources from the same
// keys matching uses.
func Index(m *typemap.Map, states []tfstate.UnitState) *tfstate.Index {
	ix := tfstate.BuildIndex(states)
	ix.DoubleManaged = New(m, ix).DoubleManaged()
	return ix
}

func keyRank(k tfstate.KeyKind) int {
	return slices.Index([]tfstate.KeyKind{tfstate.KeyIdentity, tfstate.KeyARN, tfstate.KeyID}, k)
}

// DoubleManaged returns the resources that instances of more than one state share a key for.
// When the same instances collide on several keys, only the strongest key is reported.
func (mt *Matcher) DoubleManaged() []tfstate.DoubleManaged {
	best := map[string]tfstate.DoubleManaged{}
	for k, refs := range mt.index {
		if k.kind == KeyRule || !multipleStates(refs) {
			continue
		}
		refs = slices.Clone(refs)
		slices.SortFunc(refs, compareRefs)
		group := refsKey(refs)
		if cur, ok := best[group]; ok {
			if r := keyRank(cur.Kind) - keyRank(k.kind); r < 0 || (r == 0 && cur.Key <= k.value) {
				continue
			}
		}
		best[group] = tfstate.DoubleManaged{Kind: k.kind, Type: k.tf, Key: k.value, Refs: refs}
	}
	out := slices.Collect(maps.Values(best))
	slices.SortFunc(out, func(a, b tfstate.DoubleManaged) int {
		return cmp.Or(strings.Compare(a.Type, b.Type), strings.Compare(a.Key, b.Key))
	})
	if out == nil {
		out = []tfstate.DoubleManaged{}
	}
	return out
}

func multipleStates(refs []tfstate.Ref) bool {
	for _, r := range refs[1:] {
		if r.Unit != refs[0].Unit || r.Workspace != refs[0].Workspace {
			return true
		}
	}
	return false
}

func compareRefs(a, b tfstate.Ref) int {
	return cmp.Or(strings.Compare(a.Unit, b.Unit), strings.Compare(a.Workspace, b.Workspace),
		strings.Compare(a.Address, b.Address))
}

func refsKey(refs []tfstate.Ref) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.Unit + "\x00" + r.Workspace + "\x00" + r.Address
	}
	return strings.Join(parts, "\x01")
}

// Query is what whois looks up: an ARN or a TF resource id, optionally narrowed to TF types.
type Query struct {
	Value string
	Types []string // empty means any
}

// IsARN reports whether the query is an ARN.
func (q *Query) IsARN() bool { return arn.IsARN(q.Value) }

// Resource returns the live resource an ARN query describes, with what the ARN and the typemap
// give: account, region, CloudFormation type and the name the ARN pattern captures.
func (q *Query) Resource(m *typemap.Map) inventory.CloudResource {
	r := inventory.CloudResource{Attrs: map[string]string{}}
	if !q.IsARN() {
		r.ID = q.Value
		return r
	}
	r.ARN = typemap.NormalizeARN(q.Value)
	if a, err := arn.Parse(r.ARN); err == nil {
		r.Account, r.Region = a.AccountID, a.Region
	}
	cfn, fields := m.Classify(r.ARN)
	r.CFNType = cfn
	r.Name = cmp.Or(fields["name"], fields["function_name"])
	if cfn != "" {
		for _, t := range m.ForCFN(cfn) {
			r.TFTypes = append(r.TFTypes, t.TF)
		}
	}
	return r
}

// Lookup finds the state instances that manage what q names, in the match order identity, ARN,
// then id. An ARN whose type is mapped is matched like a live resource; any other ARN, and a
// bare id, is looked up across every type.
func (mt *Matcher) Lookup(q *Query) Result {
	r := q.Resource(mt.m)
	if q.IsARN() && r.CFNType != "" && mt.m.Mapped(r.CFNType) {
		res := mt.one(&r)
		refs := mt.ofTypes(res.ManagedBy, q.Types)
		switch {
		case len(refs) == 0:
		case res.Category == CategoryDerived:
			res.ManagedBy = refs
			return res
		default:
			return decide(Result{Resource: r}, refs, res.Key)
		}
	}
	res := Result{Resource: r}
	var refs []tfstate.Ref
	kind := tfstate.KeyID
	if q.IsARN() {
		refs, kind = mt.byARN[r.ARN], tfstate.KeyARN
	} else {
		refs = mt.byID[q.Value]
	}
	if refs = mt.ofTypes(refs, q.Types); len(refs) > 0 {
		// Keys include the type, so instances of different types are different resources: an
		// ARN or id shared by a topic and its policy is ambiguous, not double-managed.
		if types := mt.typesOf(refs); len(types) > 1 {
			res.ManagedBy = refs
			slices.SortFunc(res.ManagedBy, compareRefs)
			res.Category, res.Rule = CategoryUndetermined, RuleAmbiguous
			res.Reason = "instances of " + strings.Join(types, ", ") + " have it; narrow with --type"
			return res
		}
		return decide(res, refs, kind)
	}
	if q.IsARN() && (r.CFNType == "" || !mt.m.Mapped(r.CFNType)) {
		res.Category, res.Rule = CategoryUndetermined, RuleNoMapping
		res.Reason = "no type mapping for " + cmp.Or(r.CFNType, "this ARN") + " and no state has the ARN"
		return res
	}
	res.Category, res.Rule, res.Reason = CategoryUnmatched, RuleNotInState, "no state manages it"
	return res
}

// ofTypes keeps the refs whose entry has one of types; empty types keeps every ref.
func (mt *Matcher) ofTypes(refs []tfstate.Ref, types []string) []tfstate.Ref {
	if len(types) == 0 || len(refs) == 0 {
		return refs
	}
	return slices.DeleteFunc(slices.Clone(refs), func(ref tfstate.Ref) bool {
		e, ok := mt.entries[ref]
		return !ok || !slices.Contains(types, e.Type)
	})
}

func (mt *Matcher) typesOf(refs []tfstate.Ref) []string {
	var out []string
	for _, r := range refs {
		if e, ok := mt.entries[r]; ok && !slices.Contains(out, e.Type) {
			out = append(out, e.Type)
		}
	}
	slices.Sort(out)
	return out
}

// Entry returns the index entry ref points at.
func (mt *Matcher) Entry(ref tfstate.Ref) (*tfstate.Entry, bool) {
	e, ok := mt.entries[ref]
	return e, ok
}
