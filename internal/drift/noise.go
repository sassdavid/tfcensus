package drift

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/iampolicy"
	"github.com/sassdavid/tfcensus/internal/ownership"
)

// Built-in noise rule presets.
const (
	PresetAWSTagsAll     = "aws-tags-all"
	PresetComputedHashes = "computed-hashes"
)

// Presets lists the preset names.
func Presets() []string { return []string{PresetAWSTagsAll, PresetComputedHashes} }

// computedHashAttributes are the attributes the computed-hashes preset hides on updates.
func computedHashAttributes() []string {
	return []string{
		"source_code_hash", "etag", "last_modified", "output_md5", "output_sha", "output_sha256", "output_sha512",
		"output_base64sha256", "output_base64sha512", "content_md5",
	}
}

// selector matches a change when every set field matches. A nil attr matches the whole change.
type selector struct {
	unit, typ, address string
	attr               pattern
	action             Action
}

type rule struct {
	id, name, reason, expiresDate string
	expires                       time.Time
	selectors                     []selector
}

// NoiseRules are compiled drift.noise entries and drift.never_hide.
type NoiseRules struct {
	rules     []rule
	neverHide []Action
}

// ErrNoise is wrapped by every noise rule error.
var ErrNoise = errors.New("invalid noise rule")

// NewNoiseRules validates and compiles the rules. It returns every error at once.
func NewNoiseRules(entries []config.NoiseRule, neverHide []string) (*NoiseRules, error) {
	n := &NoiseRules{}
	var errs []error
	for _, s := range neverHide {
		a, err := ParseAction(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: drift.never_hide: %w", ErrNoise, err))
			continue
		}
		n.neverHide = append(n.neverHide, a)
	}
	seen := map[string]bool{}
	for i, e := range entries {
		r, err := compileRule(i, e)
		if err == nil && seen[r.id] {
			err = fmt.Errorf("%w: drift.noise[%d]: %s is listed twice", ErrNoise, i, r.id)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		seen[r.id] = true
		n.rules = append(n.rules, r)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return n, nil
}

func compileRule(i int, e config.NoiseRule) (rule, error) {
	where := fmt.Sprintf("drift.noise[%d]", i)
	if e.Preset != "" {
		if e != (config.NoiseRule{Preset: e.Preset}) {
			return rule{}, fmt.Errorf("%w: %s: a preset entry takes no other key", ErrNoise, where)
		}
		return preset(e.Preset, where)
	}
	if e.Name == "" || e.Reason == "" {
		return rule{}, fmt.Errorf("%w: %s: set preset, or name and reason", ErrNoise, where)
	}
	where += " (" + e.Name + ")"
	if e.Unit == "" && e.Type == "" && e.Address == "" && e.Attribute == "" && e.Action == "" {
		return rule{}, fmt.Errorf("%w: %s: set unit, type, address, attribute or action", ErrNoise, where)
	}
	r := rule{id: "user/" + e.Name, name: e.Name, reason: e.Reason, expiresDate: e.Expires}
	sel := selector{unit: e.Unit, typ: e.Type, address: e.Address}
	var errs []error
	if e.Attribute != "" {
		p, err := parsePattern(e.Attribute)
		if err != nil {
			errs = append(errs, err)
		}
		sel.attr = p
	}
	if e.Action != "" {
		a, err := ParseAction(e.Action)
		if err != nil {
			errs = append(errs, err)
		}
		sel.action = a
	}
	if e.Expires != "" {
		t, err := ownership.ParseExpires(e.Expires)
		if err != nil {
			errs = append(errs, fmt.Errorf("expires %q: want YYYY-MM-DD", e.Expires))
		}
		r.expires = t
	}
	if err := errors.Join(errs...); err != nil {
		return rule{}, fmt.Errorf("%w: %s: %w", ErrNoise, where, err)
	}
	r.selectors = []selector{sel}
	return r, nil
}

func preset(name, where string) (rule, error) {
	r := rule{id: "preset/" + name, name: name}
	switch name {
	case PresetAWSTagsAll:
		r.reason = "the AWS provider copies default_tags into tags_all"
		r.selectors = []selector{{typ: "aws_*", attr: pattern{{glob: "tags_all"}}, action: ActionUpdate}}
	case PresetComputedHashes:
		r.reason = "hashes and timestamps computed from content outside the configuration"
		for _, a := range computedHashAttributes() {
			r.selectors = append(r.selectors, selector{attr: pattern{{glob: a}}, action: ActionUpdate})
		}
	default:
		return rule{}, fmt.Errorf("%w: %s: unknown preset %q: want one of %v", ErrNoise, where, name, Presets())
	}
	return r, nil
}

func (s *selector) matchesChange(c *Change) bool {
	for _, m := range []struct{ pattern, value string }{{s.unit, c.Unit}, {s.typ, c.Type}, {s.address, c.Address}} {
		if m.pattern != "" && !iampolicy.Glob(m.pattern, m.value, false) {
			return false
		}
	}
	return s.action == "" || s.action == c.Action
}

// match returns whether r matches attribute a of c, or the whole change when a is nil.
func (r *rule) match(c *Change, a *Attr) bool {
	for i := range r.selectors {
		s := &r.selectors[i]
		if !s.matchesChange(c) {
			continue
		}
		switch {
		case s.attr == nil:
			return true
		case a != nil && s.attr.matches(a.segs):
			return true
		}
	}
	return false
}

// apply hides the attributes and changes the rules match, at now, and returns warnings for the
// rules past their expires date.
func (n *NoiseRules) apply(changes []Change, now time.Time) []string {
	hits := make([]int, len(n.rules))
	for i := range changes {
		n.applyChange(&changes[i], now, hits)
	}
	var warnings []string
	for i := range n.rules {
		r := &n.rules[i]
		if ownership.Expired(r.expires, now) {
			warnings = append(warnings, fmt.Sprintf(
				"noise rule %q is past its expires date (%s) and no longer hides drift: %d change(s) reported again",
				r.name, r.expiresDate, hits[i]))
		}
	}
	return warnings
}

func (n *NoiseRules) applyChange(c *Change, now time.Time, hits []int) {
	protected := slices.Contains(n.neverHide, c.Action) || (c.Revert != "" && slices.Contains(n.neverHide, c.Revert))
	expiredHit := map[int]bool{}
	defer func() {
		for i := range expiredHit {
			hits[i]++
		}
	}()
	decide := func(a *Attr) *Noise { return n.decide(c, a, now, protected, expiredHit) }
	if len(c.Attributes) == 0 {
		c.Hidden = decide(nil)
		return
	}
	for j := range c.Attributes {
		c.Attributes[j].Noise = decide(&c.Attributes[j])
	}
	if len(c.Visible()) == 0 {
		c.Hidden = c.Attributes[0].Noise
	}
}

// decide returns the first rule in force that matches attribute a of c (the whole change when a
// is nil). Expired rules that match are recorded in expiredHit; a protected change is never hidden.
func (n *NoiseRules) decide(c *Change, a *Attr, now time.Time, protected bool, expiredHit map[int]bool) *Noise {
	for i := range n.rules {
		r := &n.rules[i]
		if !r.match(c, a) {
			continue
		}
		if ownership.Expired(r.expires, now) {
			expiredHit[i] = true
			continue
		}
		if protected {
			if c.Protected == "" {
				c.Protected = fmt.Sprintf("%s is in never_hide, so %s doesn't apply", n.protectedBy(c), r.id)
			}
			return nil
		}
		return &Noise{Rule: r.id, Reason: r.reason}
	}
	return nil
}

func (n *NoiseRules) protectedBy(c *Change) Action {
	if slices.Contains(n.neverHide, c.Action) {
		return c.Action
	}
	return c.Revert
}
