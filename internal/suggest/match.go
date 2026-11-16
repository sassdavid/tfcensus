package suggest

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	tfjson "github.com/hashicorp/terraform-json"
)

// Actions of an unmatched resource.
const (
	ActionDelete = "delete"
	ActionCreate = "create"
)

// Unit is one unit's plan and the side of a move it can be on.
type Unit struct {
	Ref  string // unit path, or path:workspace
	From bool   // its planned deletes can be move sources
	To   bool   // its planned creates can be move destinations
	Plan *tfjson.Plan
}

// Move is a planned delete and create that are the same resource.
type Move struct {
	Type        string `json:"type"`
	FromUnit    string `json:"from_unit"`
	FromAddress string `json:"from_address"`
	ToUnit      string `json:"to_unit"`
	ToAddress   string `json:"to_address"`
}

// Ref names a resource in a unit.
type Ref struct {
	Unit    string `json:"unit"`
	Address string `json:"address"`
}

// Closest is the candidate with the fewest differing attributes, by top-level attribute name.
type Closest struct {
	Ref

	Differ []string `json:"differ"`
}

// Unmatched is a planned delete or create that no move covers. Candidates lists its full matches
// when there are any (a move needs exactly one on each side); otherwise Closest is the nearest one.
type Unmatched struct {
	Ref

	Type       string   `json:"type"`
	Action     string   `json:"action"`
	Candidates []Ref    `json:"candidates"`
	Closest    *Closest `json:"closest,omitempty"`
}

// Result is what [Match] found.
type Result struct {
	Comparisons int         `json:"comparisons"`
	Moves       []Move      `json:"moves"`
	Unmatched   []Unmatched `json:"unmatched"`
}

type resource struct {
	Ref

	typ     string
	attrs   map[string]any
	unknown map[string]bool // create only: keys whose value is known after apply
}

type side struct {
	matches []int
	closest int // index of the candidate with the fewest differences, or -1
	differ  []string
}

// Match pairs the planned deletes of From units with the planned creates of To units. A pair is a
// move when its type is the same, every known attribute is equal or equated by a rule, and neither
// resource fully matches anything else. Replaced resources and data sources never take part.
func Match(units []Unit, rules []Rule) (*Result, error) {
	deletes, creates, err := collect(units)
	if err != nil {
		return nil, err
	}
	ds := make([]side, len(deletes))
	cs := make([]side, len(creates))
	for i := range ds {
		ds[i].closest = -1
	}
	for i := range cs {
		cs[i].closest = -1
	}
	res := &Result{Moves: []Move{}, Unmatched: []Unmatched{}}
	for di := range deletes {
		d := &deletes[di]
		for ci := range creates {
			c := &creates[ci]
			if c.typ != d.typ {
				continue
			}
			res.Comparisons++
			differ := compare(c, d, rules)
			if len(differ) == 0 {
				ds[di].matches = append(ds[di].matches, ci)
				cs[ci].matches = append(cs[ci].matches, di)
				continue
			}
			nearer(&ds[di], ci, differ)
			nearer(&cs[ci], di, differ)
		}
	}
	for di := range deletes {
		if m := ds[di].matches; len(m) == 1 && len(cs[m[0]].matches) == 1 {
			d, c := &deletes[di], &creates[m[0]]
			res.Moves = append(res.Moves, Move{
				Type: d.typ, FromUnit: d.Unit, FromAddress: d.Address, ToUnit: c.Unit, ToAddress: c.Address,
			})
			continue
		}
		res.Unmatched = append(res.Unmatched, unmatched(&deletes[di], ActionDelete, ds[di], creates))
	}
	for ci := range creates {
		if m := cs[ci].matches; len(m) == 1 && len(ds[m[0]].matches) == 1 {
			continue
		}
		res.Unmatched = append(res.Unmatched, unmatched(&creates[ci], ActionCreate, cs[ci], deletes))
	}
	return res, nil
}

func nearer(s *side, other int, differ []string) {
	if s.closest < 0 || len(differ) < len(s.differ) {
		s.closest, s.differ = other, differ
	}
}

func unmatched(r *resource, action string, s side, others []resource) Unmatched {
	u := Unmatched{Ref: r.Ref, Type: r.typ, Action: action, Candidates: []Ref{}}
	for _, i := range s.matches {
		u.Candidates = append(u.Candidates, others[i].Ref)
	}
	if len(s.matches) == 0 && s.closest >= 0 {
		u.Closest = &Closest{Ref: others[s.closest].Ref, Differ: s.differ}
	}
	return u
}

// compare returns the top-level names of the attributes that differ between a planned create and
// a planned delete, sorted.
func compare(c, d *resource, rules []Rule) []string {
	keys := slices.Collect(maps.Keys(c.attrs))
	for k := range d.attrs {
		if _, ok := c.attrs[k]; !ok {
			keys = append(keys, k)
		}
	}
	differ := map[string]bool{}
	for _, k := range keys {
		if covered(c.unknown, k) {
			continue
		}
		cv, cok := c.attrs[k]
		dv, dok := d.attrs[k]
		if cok && dok && cv == dv {
			continue
		}
		if !slices.ContainsFunc(rules, func(r Rule) bool { return r.appliesTo(c.typ, k) && r.equates(cv, dv) }) {
			differ[topLevel(k)] = true
		}
	}
	return slices.Sorted(maps.Keys(differ))
}

// collect returns the pure deletes of From units and the pure creates of To units, each sorted by
// unit and address so that the result doesn't depend on plan order.
func collect(units []Unit) ([]resource, []resource, error) {
	var deletes, creates []resource
	for _, u := range units {
		if u.Plan == nil {
			continue
		}
		for _, rc := range u.Plan.ResourceChanges {
			r, action, err := classify(&u, rc)
			switch {
			case err != nil:
				return nil, nil, err
			case action == ActionDelete:
				deletes = append(deletes, r)
			case action == ActionCreate:
				creates = append(creates, r)
			}
		}
	}
	byRef := func(a, b resource) int { return cmp.Or(cmp.Compare(a.Unit, b.Unit), cmp.Compare(a.Address, b.Address)) }
	slices.SortFunc(deletes, byRef)
	slices.SortFunc(creates, byRef)
	return deletes, creates, nil
}

// classify returns a resource change as a move source (ActionDelete), a move destination
// (ActionCreate), or neither ("").
func classify(u *Unit, rc *tfjson.ResourceChange) (resource, string, error) {
	if rc == nil || rc.Change == nil || rc.Mode == tfjson.DataResourceMode {
		return resource{}, "", nil
	}
	switch {
	case u.From && only(rc.Change.Actions, tfjson.ActionDelete):
		r, err := newResource(u.Ref, rc, rc.Change.Before, nil)
		return r, ActionDelete, err
	case u.To && only(rc.Change.Actions, tfjson.ActionCreate):
		r, err := newResource(u.Ref, rc, rc.Change.After, rc.Change.AfterUnknown)
		return r, ActionCreate, err
	}
	return resource{}, "", nil
}

func only(actions tfjson.Actions, a tfjson.Action) bool {
	return len(actions) == 1 && actions[0] == a
}

// newResource flattens a planned value. The error names the address only, never the value.
func newResource(unit string, rc *tfjson.ResourceChange, value, unknown any) (resource, error) {
	r := resource{Unit: unit, Address: rc.Address, typ: rc.Type, attrs: map[string]any{}}
	switch value.(type) {
	case nil, map[string]any:
	default:
		return resource{}, fmt.Errorf("plan of %s: %s is not an object", unit, rc.Address)
	}
	flatten("", value, r.attrs)
	if unknown != nil {
		r.unknown = map[string]bool{}
		marks("", unknown, r.unknown)
	}
	return r, nil
}
