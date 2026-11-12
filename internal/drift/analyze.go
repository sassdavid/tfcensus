package drift

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Options configures [Analyze].
type Options struct {
	Mode           Mode
	Collapse       Collapse
	Noise          *NoiseRules // nil hides nothing
	ShowAttributes bool        // keep before and after values, masked when sensitive or unknown
	Now            time.Time   // for noise rule expiry
}

// Analyze diffs every plan, applies the noise rules and groups the visible changes.
func Analyze(inputs []Input, warnings []string, opts Options) *Report {
	rep := &Report{
		Mode: opts.Mode, Collapse: opts.Collapse, Units: make([]Unit, 0, len(inputs)), Groups: []Group{},
		Changes: []Change{}, Warnings: append([]string{}, warnings...),
	}
	for _, in := range inputs {
		u := in.Unit
		if in.Plan != nil {
			res := fromPlan(u.Path, in.Plan, opts.Mode, opts.ShowAttributes)
			u.Pending, u.Outputs = res.pending, res.outputs
			rep.Changes = append(rep.Changes, res.changes...)
		}
		rep.Units = append(rep.Units, u)
	}
	slices.SortStableFunc(rep.Changes, func(a, b Change) int {
		return cmp.Or(strings.Compare(a.Unit, b.Unit), strings.Compare(a.Address, b.Address))
	})
	if opts.Noise != nil {
		rep.Warnings = append(rep.Warnings, opts.Noise.apply(rep.Changes, opts.Now)...)
	}
	rep.Groups = collapse(rep.Changes, opts.Collapse)
	summarize(rep)
	return rep
}

func summarize(rep *Report) {
	byUnit := map[string]*Unit{}
	for i := range rep.Units {
		byUnit[rep.Units[i].Path] = &rep.Units[i]
	}
	s := &rep.Summary
	for i := range rep.Changes {
		c := &rep.Changes[i]
		u := byUnit[c.Unit]
		switch {
		case c.Hidden != nil:
			u.Hidden++
			s.Hidden++
		default:
			u.Drifted++
			s.Resources++
			if c.Revert != "" {
				u.Reverts++
				s.Reverts++
			}
		}
	}
	s.Units, s.Groups = len(rep.Units), len(rep.Groups)
	for i := range rep.Units {
		u := &rep.Units[i]
		if u.Status == StatusClean && u.Drifted > 0 {
			u.Status = StatusDrifted
		}
		switch u.Status {
		case StatusDrifted:
			s.Drifted++
		case StatusClean:
			s.Clean++
		case StatusFailed:
			s.Failed++
		case StatusEarlyExit:
			s.EarlyExit++
		case StatusExcluded:
			s.Excluded++
		}
	}
}

// groupKey identifies the changes collapse puts together. The strict key holds values and stays in
// memory.
func groupKey(c *Change, mode Collapse) string {
	parts := []string{c.Type, string(c.Action), string(c.Revert)}
	switch mode {
	case CollapseNone:
		return c.Label()
	case CollapseStrict:
		for _, a := range c.Visible() {
			parts = append(parts, a.Path+"\x00"+string(a.Kind)+"\x00"+a.value)
		}
	case CollapseShape:
		var paths []string
		for _, a := range c.Visible() {
			paths = append(paths, a.segs.format(true))
		}
		slices.Sort(paths)
		parts = append(parts, slices.Compact(paths)...)
	}
	return strings.Join(parts, "\x01")
}

// collapse groups the visible changes and sets each change's Group. Groups are ordered by size,
// then type, action and first member; IDs are g1, g2 and so on in that order.
func collapse(changes []Change, mode Collapse) []Group {
	type acc struct {
		group   Group
		changes []*Change
	}
	byKey := map[string]*acc{}
	var order []*acc
	for i := range changes {
		c := &changes[i]
		if c.Hidden != nil {
			continue
		}
		k := groupKey(c, mode)
		g, ok := byKey[k]
		if !ok {
			g = &acc{group: Group{Type: c.Type, Action: c.Action, Revert: c.Revert}}
			byKey[k] = g
			order = append(order, g)
		}
		g.group.Members = append(g.group.Members, Member{Unit: c.Unit, Address: c.Address})
		g.changes = append(g.changes, c)
	}
	slices.SortStableFunc(order, func(a, b *acc) int {
		return cmp.Or(
			cmp.Compare(len(b.changes), len(a.changes)), strings.Compare(a.group.Type, b.group.Type),
			strings.Compare(string(a.group.Action), string(b.group.Action)),
			strings.Compare(a.changes[0].Label(), b.changes[0].Label()),
		)
	})
	out := make([]Group, 0, len(order))
	for i, g := range order {
		g.group.ID = "g" + strconv.Itoa(i+1)
		g.group.Attributes = groupAttrs(g.changes, mode == CollapseShape)
		for _, c := range g.changes {
			c.Group = g.group.ID
		}
		out = append(out, g.group)
	}
	return out
}

// groupAttrs merges the members' visible attributes by path (shape paths with shape). A kind or
// value the members don't share becomes KindMixed or is dropped.
func groupAttrs(changes []*Change, shape bool) []Attr {
	byPath := map[string]*Attr{}
	var paths []string
	for ci, c := range changes {
		for _, a := range c.Visible() {
			p := a.segs.format(shape)
			m, ok := byPath[p]
			if !ok {
				cp := a
				cp.Path, cp.Noise = p, nil
				if ci > 0 {
					cp.Before, cp.After = nil, nil
				}
				byPath[p] = &cp
				paths = append(paths, p)
				continue
			}
			merge(m, &a)
		}
	}
	slices.Sort(paths)
	out := make([]Attr, 0, len(paths))
	for _, p := range paths {
		out = append(out, *byPath[p])
	}
	return out
}

func merge(m, a *Attr) {
	if m.Kind != a.Kind {
		m.Kind = KindMixed
	}
	m.Sensitive = m.Sensitive || a.Sensitive
	m.Unknown = m.Unknown || a.Unknown
	m.ForcesReplacement = m.ForcesReplacement || a.ForcesReplacement
	if !sameValue(m.Before, a.Before) || !sameValue(m.After, a.After) {
		m.Before, m.After = nil, nil
	}
}

func sameValue(a, b *string) bool { return a != nil && b != nil && *a == *b }
