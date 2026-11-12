package notify

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/report"
	"github.com/sassdavid/tfcensus/internal/scan"
)

// Notifier names.
const (
	NameSlack     = "slack"
	NameGitHub    = "github-issues"
	NameBitbucket = "bitbucket-pr-comment"
	NameFile      = "file"
)

// Names lists every notifier.
func Names() []string { return []string{NameSlack, NameGitHub, NameBitbucket, NameFile} }

// FleetScope is the scope of audit findings that name no account.
const FleetScope = "fleet"

// ErrAuth is wrapped when an API refuses the token.
var ErrAuth = errors.New("authentication failed")

// ErrConfig is wrapped when a notifier lacks a setting or a secret.
var ErrConfig = errors.New("notifier not configured")

// Notifier publishes a report. Publishing the same report twice changes nothing the second time.
type Notifier interface {
	Name() string
	Publish(ctx context.Context, r *Report) (*Result, error)
}

// Report is what the notifiers publish: one saved report split into scopes.
type Report struct {
	Kind        report.Kind `json:"kind"`
	GeneratedAt time.Time   `json:"-"` // the run's time; left out of bodies so re-runs compare equal
	Scopes      []Scope     `json:"scopes"`
}

// Scope is a unit (drift) or an account (scan, audit). Every scope the run covered is listed, so
// a scope with no items and no failure is resolved.
type Scope struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Failed bool   `json:"failed,omitempty"` // the drift plan failed, so drift is unknown
	Reason string `json:"reason,omitempty"`
	Items  []Item `json:"items"`
}

// Open reports whether the scope has something to report.
func (s *Scope) Open() bool { return s.Failed || len(s.Items) > 0 }

// Item is one finding: identifiers and the rule that decided it, never attribute values.
type Item struct {
	Level  string `json:"level"` // scan category, audit severity or drift action
	Type   string `json:"type"`
	Target string `json:"target"` // ARN or ID, audit target, or resource address
	Region string `json:"region,omitempty"`
	Rule   string `json:"rule,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// OpenScopes returns the scopes with something to report.
func (r *Report) OpenScopes() []Scope {
	var out []Scope
	for _, s := range r.Scopes {
		if s.Open() {
			out = append(out, s)
		}
	}
	return out
}

// Items counts the items of every scope.
func (r *Report) Items() int {
	n := 0
	for _, s := range r.Scopes {
		n += len(s.Items)
	}
	return n
}

// Options selects what a report keeps.
type Options struct {
	Categories  []string // scan categories; empty means double-managed and unmanaged
	MinSeverity string   // lowest audit severity; empty means low
}

// FromSaved turns a saved scan, drift or audit envelope into a report.
func FromSaved(s *report.Saved, o Options) (*Report, error) {
	r := &Report{Kind: s.Kind, GeneratedAt: s.GeneratedAt}
	switch s.Kind {
	case report.KindScan:
		r.Scopes = scanScopes(s.Scan, o)
	case report.KindDrift:
		r.Scopes = driftScopes(s.Drift)
	case report.KindAudit:
		r.Scopes = auditScopes(s.Audit, o)
	case report.KindIndex:
		return nil, fmt.Errorf("%s is a state index; notifiers publish scan, drift and audit reports", s.Path)
	}
	slices.SortFunc(r.Scopes, func(a, b Scope) int { return strings.Compare(a.Key, b.Key) })
	return r, nil
}

func scanScopes(rep *scan.Report, o Options) []Scope {
	cats := o.Categories
	if len(cats) == 0 {
		cats = []string{string(scan.CategoryDoubleManaged), string(scan.CategoryUnmanaged)}
	}
	byAccount := map[string]*Scope{}
	var order []string
	for _, a := range rep.Accounts {
		byAccount[a.Account] = &Scope{
			Key:   a.Account,
			Title: "tfcensus: unmanaged resources in account " + a.Account,
			Items: []Item{},
		}
		order = append(order, a.Account)
	}
	for i := range rep.Findings {
		f := &rep.Findings[i]
		sc := byAccount[f.Account]
		if sc == nil || !slices.Contains(cats, string(f.Category)) {
			continue
		}
		detail := f.Reason
		if f.Owner != "" {
			detail += "; owner " + f.Owner
		}
		sc.Items = append(sc.Items, Item{
			Level: string(f.Category), Type: f.Type(), Target: f.Key(), Region: f.Region, Rule: f.Rule, Detail: detail,
		})
	}
	out := make([]Scope, 0, len(order))
	for _, k := range order {
		out = append(out, *byAccount[k])
	}
	return out
}

func driftScopes(rep *drift.Report) []Scope {
	byUnit := map[string]*Scope{}
	var order []string
	for _, u := range rep.Units {
		// Early-exit and excluded units weren't planned, so their drift is unknown and an open
		// issue for them stays as it is.
		if u.Status != drift.StatusDrifted && u.Status != drift.StatusClean && u.Status != drift.StatusFailed {
			continue
		}
		sc := &Scope{Key: u.Path, Title: "tfcensus: drift in " + u.Path, Items: []Item{}}
		if u.Status == drift.StatusFailed {
			sc.Failed, sc.Reason = true, cmp.Or(u.Reason, "plan failed")
		}
		byUnit[u.Path] = sc
		order = append(order, u.Path)
	}
	for _, c := range rep.VisibleChanges() {
		sc := byUnit[c.Unit]
		if sc == nil {
			continue
		}
		paths := make([]string, 0, len(c.Attributes))
		for _, a := range c.Visible() {
			paths = append(paths, a.Path)
		}
		level := string(c.Action)
		if c.Revert != "" {
			level += ", plan reverts with " + string(c.Revert)
		}
		sc.Items = append(
			sc.Items,
			Item{Level: level, Type: c.Type, Target: c.Address, Detail: strings.Join(paths, ", ")},
		)
	}
	out := make([]Scope, 0, len(order))
	for _, k := range order {
		out = append(out, *byUnit[k])
	}
	return out
}

func auditScopes(rep *audit.Report, o Options) []Scope {
	minRank := report.SeverityRank(audit.Severity(cmp.Or(o.MinSeverity, string(audit.SeverityLow))))
	byAccount := map[string]*Scope{}
	scope := func(acct string) *Scope {
		key := cmp.Or(acct, FleetScope)
		if byAccount[key] == nil {
			title := "tfcensus: backend audit findings in account " + key
			if key == FleetScope {
				title = "tfcensus: backend audit findings in the fleet"
			}
			byAccount[key] = &Scope{Key: key, Title: title, Items: []Item{}}
		}
		return byAccount[key]
	}
	scope("")
	for _, b := range rep.Buckets {
		scope(b.Account)
	}
	for _, f := range rep.Findings {
		sc := scope(f.Account)
		if report.SeverityRank(f.Severity) > minRank {
			continue
		}
		sc.Items = append(
			sc.Items,
			Item{
				Level:  string(f.Severity),
				Type:   "backend",
				Target: f.Target,
				Region: f.Region,
				Rule:   f.Rule,
				Detail: f.Reason,
			},
		)
	}
	out := make([]Scope, 0, len(byAccount))
	for _, sc := range byAccount {
		out = append(out, *sc)
	}
	return out
}

// Op is what a notifier did, or would do with --dry-run-notify, for one scope.
type Op string

// Operations.
const (
	OpCreate    Op = "create"
	OpUpdate    Op = "update"
	OpClose     Op = "close"
	OpUnchanged Op = "unchanged"
	OpSkipped   Op = "skipped" // max_open reached, or nothing to send
	OpSend      Op = "send"
	OpWrite     Op = "write"
)

// Action is one operation on one scope.
type Action struct {
	Op     Op          `json:"op"`
	Kind   report.Kind `json:"kind"`
	Scope  string      `json:"scope"`
	Title  string      `json:"title,omitempty"`
	Detail string      `json:"detail,omitempty"`
	URL    string      `json:"url,omitempty"`
}

// Result lists what one notifier did.
type Result struct {
	Notifier string   `json:"notifier"`
	Target   string   `json:"target"` // repository, pull request, webhook or directory
	DryRun   bool     `json:"dry_run"`
	Actions  []Action `json:"actions"`
}

func (r *Result) add(a Action) { r.Actions = append(r.Actions, a) }

// Changed counts the actions that write something.
func (r *Result) Changed() int {
	n := 0
	for _, a := range r.Actions {
		if a.Op != OpUnchanged && a.Op != OpSkipped {
			n++
		}
	}
	return n
}
