package drift

import (
	"fmt"
	"slices"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
)

// Mode says which plan drift reads.
type Mode string

// Modes, set by drift.mode or --mode.
const (
	ModeRefreshOnly Mode = "refresh-only" // plan -refresh-only: drift only
	ModeFull        Mode = "full"         // plan: drift, and whether the plan reverts it
)

// ParseMode validates a drift.mode value.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeRefreshOnly, ModeFull:
		return m, nil
	default:
		return "", fmt.Errorf("invalid mode %q: want refresh-only or full", s)
	}
}

// Collapse says how changes in several units become one group.
type Collapse string

// Collapse modes, set by drift.collapse or --collapse.
const (
	CollapseShape  Collapse = "shape"  // same type, action and attribute paths; values ignored
	CollapseStrict Collapse = "strict" // shape, and the same before and after values
	CollapseNone   Collapse = "none"   // one group per change
)

// ParseCollapse validates a drift.collapse value.
func ParseCollapse(s string) (Collapse, error) {
	switch c := Collapse(s); c {
	case CollapseShape, CollapseStrict, CollapseNone:
		return c, nil
	default:
		return "", fmt.Errorf("invalid collapse %q: want shape, strict or none", s)
	}
}

// Action is what a change does to a resource.
type Action string

// Actions. A plan's no-op, read and forget entries aren't changes.
const (
	ActionCreate  Action = "create"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionReplace Action = "replace"
)

// Actions lists every action.
func Actions() []Action { return []Action{ActionCreate, ActionUpdate, ActionDelete, ActionReplace} }

// ParseAction validates an action name.
func ParseAction(s string) (Action, error) {
	if a := Action(s); slices.Contains(Actions(), a) {
		return a, nil
	}
	return "", fmt.Errorf("invalid action %q: want create, update, delete or replace", s)
}

func actionOf(a tfjson.Actions) (Action, bool) {
	switch {
	case a.Replace():
		return ActionReplace, true
	case a.Create():
		return ActionCreate, true
	case a.Update():
		return ActionUpdate, true
	case a.Delete():
		return ActionDelete, true
	default:
		return "", false
	}
}

// Status is the outcome of one unit.
type Status string

// Unit statuses. early-exit and excluded units are reported but aren't errors.
const (
	StatusDrifted   Status = "drifted"    // at least one change that no noise rule hides
	StatusClean     Status = "clean"      // no drift, or only hidden drift
	StatusFailed    Status = "failed"     // the plan failed, is missing or can't be read
	StatusEarlyExit Status = "early-exit" // a dependency failed, so terragrunt skipped it
	StatusExcluded  Status = "excluded"   // an exclude block or a filter skipped it
)

// Kind says how an attribute changed.
type Kind string

// Attribute change kinds. KindMixed only appears on groups whose members differ.
const (
	KindAdded   Kind = "added"
	KindRemoved Kind = "removed"
	KindChanged Kind = "changed"
	KindMixed   Kind = "mixed"
)

// Masks shown instead of values.
const (
	MaskSensitive = "(sensitive)"
	MaskUnknown   = "(known after apply)"
)

// Unit is the outcome of one unit's plan.
type Unit struct {
	Path    string   `json:"path"` // relative to the plan directory, prefixed with the repo in run mode
	Account string   `json:"account,omitempty"`
	Region  string   `json:"region,omitempty"`
	Status  Status   `json:"status"`
	Reason  string   `json:"reason,omitempty"` // why the unit failed or was skipped
	Drifted int      `json:"drifted"`          // changes no noise rule hides
	Reverts int      `json:"reverts"`          // drifted changes the plan reverts (full mode)
	Hidden  int      `json:"hidden"`           // changes noise rules hide
	Pending int      `json:"pending"`          // planned changes that aren't drift (full mode)
	Outputs []string `json:"outputs"`          // names of the outputs that changed
}

// Change is one drifted resource.
type Change struct {
	Unit       string `json:"unit"`
	Address    string `json:"address"`
	Type       string `json:"type"`
	Action     Action `json:"action"`           // what changed outside terraform
	Revert     Action `json:"revert,omitempty"` // what the plan does about it (full mode); empty when it keeps the drift
	Attributes []Attr `json:"attributes"`
	Hidden     *Noise `json:"hidden,omitempty"`    // the rule that hides the whole change
	Protected  string `json:"protected,omitempty"` // why a matching noise rule didn't hide it
	Group      string `json:"group,omitempty"`
}

// Visible returns the attributes no noise rule hides.
func (c *Change) Visible() []Attr {
	out := make([]Attr, 0, len(c.Attributes))
	for _, a := range c.Attributes {
		if a.Noise == nil {
			out = append(out, a)
		}
	}
	return out
}

// Label is the unit and address, as one string.
func (c *Change) Label() string { return c.Unit + " " + c.Address }

// Attr is one changed attribute path. Before and After are set only with --show-attributes, and
// are masked when the value is sensitive or unknown.
type Attr struct {
	Path              string  `json:"path"`
	Kind              Kind    `json:"kind"`
	Sensitive         bool    `json:"sensitive,omitempty"`
	Unknown           bool    `json:"unknown,omitempty"`
	ForcesReplacement bool    `json:"forces_replacement,omitempty"`
	Before            *string `json:"before,omitempty"`
	After             *string `json:"after,omitempty"`
	Noise             *Noise  `json:"noise,omitempty"` // the rule that hides this attribute

	segs  path
	value string // canonical before and after for --collapse strict; never written out
}

// Noise names the rule that hid drift.
type Noise struct {
	Rule   string `json:"rule"` // preset/<name> or user/<name>
	Reason string `json:"reason"`
}

// Group is a set of changes with the same shape, or the same values with --collapse strict.
type Group struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Action     Action   `json:"action"`
	Revert     Action   `json:"revert,omitempty"`
	Attributes []Attr   `json:"attributes"`
	Members    []Member `json:"members"`
}

// Member is one change of a group.
type Member struct {
	Unit    string `json:"unit"`
	Address string `json:"address"`
}

// Summary counts units by status and changes by visibility.
type Summary struct {
	Units     int `json:"units"`
	Drifted   int `json:"drifted"`
	Clean     int `json:"clean"`
	Failed    int `json:"failed"`
	EarlyExit int `json:"early_exit"`
	Excluded  int `json:"excluded"`
	Resources int `json:"resources"` // changes no noise rule hides
	Reverts   int `json:"reverts"`
	Hidden    int `json:"hidden"`
	Groups    int `json:"groups"`
}

// Report is the result of a drift run.
type Report struct {
	Mode     Mode     `json:"mode"`
	Collapse Collapse `json:"collapse"`
	Summary  Summary  `json:"summary"`
	Units    []Unit   `json:"units"`
	Groups   []Group  `json:"groups"`
	Changes  []Change `json:"changes"`
	Warnings []string `json:"warnings"`
}

// FailedUnits returns the units whose plan failed, in report order.
func (r *Report) FailedUnits() []Unit {
	var out []Unit
	for _, u := range r.Units {
		if u.Status == StatusFailed {
			out = append(out, u)
		}
	}
	return out
}

// VisibleChanges returns the changes no noise rule hides, in report order.
func (r *Report) VisibleChanges() []*Change {
	var out []*Change
	for i := range r.Changes {
		if r.Changes[i].Hidden == nil {
			out = append(out, &r.Changes[i])
		}
	}
	return out
}

// Text is the line summary of the report.
func (s *Summary) Text() string {
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{
		{s.Drifted, "drifted"},
		{s.Clean, "clean"},
		{s.Failed, "failed"},
		{s.EarlyExit, "early exit"},
		{s.Excluded, "excluded"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	units := fmt.Sprintf("%d units", s.Units)
	if len(parts) > 0 {
		units += " (" + strings.Join(parts, ", ") + ")"
	}
	line := fmt.Sprintf("%s; %d drifted resources in %d groups", units, s.Resources, s.Groups)
	if s.Reverts > 0 {
		line += fmt.Sprintf(", %d reverted by the plan", s.Reverts)
	}
	return line + fmt.Sprintf("; %d hidden by noise rules", s.Hidden)
}
