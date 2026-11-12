package whois

import (
	"slices"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Live says what the ownership rules saw of a resource no state manages.
type Live string

// Live outcomes.
const (
	LiveNotNeeded     Live = "not-needed"     // a state manages it
	LiveOffline       Live = "offline"        // the rules saw only what the query gives
	LiveFound         Live = "found"          // the rules saw the live resource
	LiveNotFound      Live = "not-found"      // the inventory listed the type and the resource isn't there
	LiveNotListed     Live = "not-listed"     // no layer listed the type in that account and region
	LiveNoCredentials Live = "no-credentials" // no configured account is the resource's account
)

// RuleNotFound is the rule of a finding whose resource the live inventory doesn't have.
const RuleNotFound = "whois/not-found"

// Manager is a state instance that manages the resource.
type Manager struct {
	Unit      string `json:"unit"` // repo/path
	Workspace string `json:"workspace"`
	Address   string `json:"address"`
	Type      string `json:"type"`
	Account   string `json:"account,omitempty"`
	Region    string `json:"region,omitempty"`
}

// Answer is what whois found for one query.
type Answer struct {
	Query     string          `json:"query"`
	Finding   scan.Finding    `json:"finding"`
	Key       tfstate.KeyKind `json:"key,omitempty"` // the match key: identity, arn or id
	ManagedBy []Manager       `json:"managed_by"`
	Live      Live            `json:"live"`
}

// Resolver answers queries.
type Resolver struct {
	Matcher *match.Matcher
	Map     *typemap.Map
	Engine  *ownership.Engine
}

// Resolve looks q up in the state index. When no state manages it, the ownership rules run on
// what q gives, such as an ARN's type, account and name.
func (rv *Resolver) Resolve(q *match.Query) Answer {
	res := rv.Matcher.Lookup(q)
	a := rv.answer(q, &res, nil)
	if !managed(res.Category) && res.Rule != match.RuleAmbiguous {
		a.Live = LiveOffline
	}
	return a
}

// NeedsLive reports whether a live lookup could tell more: no state manages the resource, and
// its ARN gives an account and a mapped type to list.
func (rv *Resolver) NeedsLive(a *Answer) bool {
	f := &a.Finding
	return a.Live == LiveOffline && f.ARN != "" && f.Account != "" && rv.Map.Mapped(f.CFNType)
}

// Refine updates a with the live resource: it matches it again, since live attributes can give
// an identity the ARN can't, then runs the rules with the inventory's tags and relations. A nil
// live resource means the inventory doesn't have it; listed says whether a layer listed the type.
func (rv *Resolver) Refine(a *Answer, q *match.Query, live *inventory.CloudResource, listed bool, ix *ownership.Index) {
	if live == nil {
		if !listed {
			a.Live = LiveNotListed
			return
		}
		a.Live = LiveNotFound
		a.Finding.Category, a.Finding.Rule, a.Finding.Owner = scan.CategoryUndetermined, RuleNotFound, ""
		a.Finding.Reason = "the live inventory of its account and region doesn't have it"
		return
	}
	res := rv.Matcher.Match([]inventory.CloudResource{*live})[0]
	if refs := managedOfTypes(rv.Matcher, res.ManagedBy, q.Types); managed(res.Category) && len(refs) == 0 {
		res = rv.Matcher.Lookup(q)
	}
	*a = rv.answer(q, &res, ix)
	if !managed(res.Category) {
		a.Live = LiveFound
	}
}

func (rv *Resolver) answer(q *match.Query, res *match.Result, ix *ownership.Index) Answer {
	var f scan.Finding
	if res.Rule == match.RuleAmbiguous {
		// States hold it, so no ownership rule may decide; the user narrows with --type.
		r := &res.Resource
		f = scan.Finding{
			Account: r.Account, Region: r.Region, Category: scan.CategoryUndetermined, CFNType: r.CFNType,
			TFTypes: r.TFTypes, ARN: r.ARN, ID: r.ID, Name: r.Name, Reason: res.Reason, Rule: res.Rule,
			ManagedBy: res.ManagedBy,
		}
	} else {
		f = scan.Classify([]match.Result{*res}, rv.Engine, rv.Map, ix)[0]
	}
	a := Answer{Query: q.Value, Finding: f, Key: res.Key, ManagedBy: []Manager{}, Live: LiveNotNeeded}
	for _, ref := range res.ManagedBy {
		m := Manager{Unit: ref.Unit, Workspace: ref.Workspace, Address: ref.Address}
		if e, ok := rv.Matcher.Entry(ref); ok {
			m.Type, m.Account, m.Region = e.Type, e.Account, e.Region
		}
		a.ManagedBy = append(a.ManagedBy, m)
	}
	return a
}

func managed(c match.Category) bool {
	return c == match.CategoryManaged || c == match.CategoryDoubleManaged || c == match.CategoryDerived
}

func managedOfTypes(mt *match.Matcher, refs []tfstate.Ref, types []string) []tfstate.Ref {
	if len(types) == 0 {
		return refs
	}
	var out []tfstate.Ref
	for _, r := range refs {
		if e, ok := mt.Entry(r); ok && slices.Contains(types, e.Type) {
			out = append(out, r)
		}
	}
	return out
}

// Listed reports whether a layer listed cfn in the account and region (the account's global
// column for a global type) without failing.
func Listed(cov *inventory.Coverage, account, region, cfn string) bool {
	if cov == nil {
		return false
	}
	for _, row := range cov.Rows {
		if row.CFNType != cfn {
			continue
		}
		want := region
		if row.Global {
			want = inventory.GlobalColumn
		}
		for i, col := range cov.Columns {
			if col.Account != account || col.Region != want || i >= len(row.Cells) {
				continue
			}
			return slices.ContainsFunc(row.Cells[i], func(l string) bool { return l != "" && l[len(l)-1] != '!' })
		}
	}
	return false
}
