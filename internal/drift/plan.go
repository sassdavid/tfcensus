package drift

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	tfjson "github.com/hashicorp/terraform-json"
)

// readPlan parses a tfplan.json. Fields that newer terraform or OpenTofu versions add are ignored.
func readPlan(file string) (*tfjson.Plan, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	var p tfjson.Plan
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	return &p, nil
}

// planResult is what one unit's plan contains.
type planResult struct {
	changes []Change
	pending int
	outputs []string
}

// fromPlan lists the drifted managed resources of p. In full mode it also matches each one with
// the plan's change for the same address.
func fromPlan(unit string, p *tfjson.Plan, mode Mode, show bool) planResult {
	var res planResult
	for _, rc := range p.ResourceDrift {
		action, ok := changeAction(rc)
		if !ok {
			continue
		}
		c := Change{Unit: unit, Address: rc.Address, Type: rc.Type, Action: action, Attributes: []Attr{}}
		if ch := rc.Change; ch.Before != nil && ch.After != nil {
			c.Attributes = diffValues(ch.Before, ch.After, ch.BeforeSensitive, ch.AfterSensitive, ch.AfterUnknown, show)
		}
		res.changes = append(res.changes, c)
	}
	if mode == ModeFull {
		res.pending = matchReverts(res.changes, p.ResourceChanges)
	}
	for name, oc := range p.OutputChanges {
		if oc == nil {
			continue
		}
		if _, ok := actionOf(oc.Actions); ok {
			res.outputs = append(res.outputs, name)
		}
	}
	slices.Sort(res.outputs)
	if res.outputs == nil {
		res.outputs = []string{}
	}
	return res
}

func changeAction(rc *tfjson.ResourceChange) (Action, bool) {
	if rc == nil || rc.Change == nil || rc.Mode == tfjson.DataResourceMode {
		return "", false
	}
	return actionOf(rc.Change.Actions)
}

// matchReverts sets Revert on each drifted change the plan undoes and marks the attributes that
// force replacement. It returns the planned changes that aren't drift. A planned update reverts
// drift only when it touches a drifted attribute; create, delete and replace always do.
func matchReverts(changes []Change, planned []*tfjson.ResourceChange) int {
	byAddr := map[string]*tfjson.ResourceChange{}
	for _, rc := range planned {
		if _, ok := changeAction(rc); ok {
			byAddr[rc.Address] = rc
		}
	}
	used := map[string]bool{}
	for i := range changes {
		c := &changes[i]
		rc, ok := byAddr[c.Address]
		if !ok {
			continue
		}
		if revert(c, rc) {
			used[c.Address] = true
		}
	}
	return len(byAddr) - len(used)
}

// revert marks the attributes of c that rc's replace_paths cover, and sets c.Revert when rc undoes
// the drift.
func revert(c *Change, rc *tfjson.ResourceChange) bool {
	action, _ := changeAction(rc)
	ch := rc.Change
	var replace []path
	for _, raw := range ch.ReplacePaths {
		if steps, isList := raw.([]any); isList {
			if p, valid := pathFromSteps(steps); valid {
				replace = append(replace, p)
			}
		}
	}
	for j := range c.Attributes {
		a := &c.Attributes[j]
		a.ForcesReplacement = slices.ContainsFunc(replace, a.segs.overlaps)
	}
	if action == ActionUpdate && len(c.Attributes) > 0 {
		planAttrs := diffValues(ch.Before, ch.After, ch.BeforeSensitive, ch.AfterSensitive, ch.AfterUnknown, false)
		if !touches(c.Attributes, planAttrs) {
			return false
		}
	}
	c.Revert = action
	return true
}

func touches(drifted, planned []Attr) bool {
	for _, d := range drifted {
		for _, p := range planned {
			if d.segs.overlaps(p.segs) {
				return true
			}
		}
	}
	return false
}
