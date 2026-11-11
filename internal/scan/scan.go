package scan

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Category is the scan's verdict on a live resource.
type Category string

// Categories of a [Finding].
const (
	CategoryManaged       Category = "managed"
	CategoryDoubleManaged Category = "double-managed"
	CategoryUnmanaged     Category = "unmanaged"
	CategoryOtherIaC      Category = "other-iac"
	CategoryAWSManaged    Category = "aws-managed"
	CategoryDerived       Category = "derived"
	CategoryIgnored       Category = "ignored"
	CategoryUndetermined  Category = "undetermined"
)

// Categories returns every category in report order.
func Categories() []Category {
	return []Category{
		CategoryManaged, CategoryDoubleManaged, CategoryUnmanaged, CategoryOtherIaC, CategoryAWSManaged,
		CategoryDerived, CategoryIgnored, CategoryUndetermined,
	}
}

// Finding is one live resource and its category. Rule names the match or ownership rule that
// decided it.
type Finding struct {
	Account    string            `json:"account"`
	Region     string            `json:"region,omitempty"`
	Category   Category          `json:"category"`
	CFNType    string            `json:"cfn_type,omitempty"`
	SourceType string            `json:"source_type,omitempty"`
	TFTypes    []string          `json:"tf_types,omitempty"`
	ARN        string            `json:"arn,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Created    string            `json:"created,omitempty"`
	Sources    []string          `json:"sources"`
	Owner      string            `json:"owner,omitempty"`
	Reason     string            `json:"reason"`
	Rule       string            `json:"rule"`
	ManagedBy  []tfstate.Ref     `json:"managed_by,omitempty"`
}

// Type returns the CloudFormation type, else the layer's own type name.
func (f *Finding) Type() string { return cmp.Or(f.CFNType, f.SourceType, "unknown") }

// Key returns the ARN, else the ID.
func (f *Finding) Key() string { return cmp.Or(f.ARN, f.ID) }

// Classify turns match results into findings. Unmatched and undetermined resources go through
// the ownership rules; a rule that matches sets the category, else unmatched resources are
// unmanaged and undetermined ones stay undetermined.
func Classify(results []match.Result, eng *ownership.Engine, m *typemap.Map, ix *ownership.Index) []Finding {
	out := make([]Finding, len(results))
	for i := range results {
		out[i] = classify(&results[i], eng, m, ix)
	}
	return out
}

func classify(r *match.Result, eng *ownership.Engine, m *typemap.Map, ix *ownership.Index) Finding {
	res := &r.Resource
	f := Finding{
		Account: res.Account, Region: res.Region, CFNType: res.CFNType, SourceType: res.SourceType,
		TFTypes: res.TFTypes, ARN: res.ARN, ID: res.ID, Name: res.Name, Tags: res.Tags, Created: res.Created,
		Sources: res.Sources, Reason: r.Reason, Rule: r.Rule, ManagedBy: r.ManagedBy,
	}
	switch r.Category {
	case match.CategoryManaged:
		f.Category = CategoryManaged
		return f
	case match.CategoryDoubleManaged:
		f.Category = CategoryDoubleManaged
		return f
	case match.CategoryDerived:
		f.Category = CategoryDerived
		return f
	case match.CategoryUnmatched, match.CategoryUndetermined:
	}
	in := ownership.FromCloud(res, m, ix)
	f.Name = in.Name
	d := eng.Evaluate(in, nil, false)
	switch {
	case d.Matched:
		f.Category, f.Owner, f.Reason, f.Rule = Category(d.Category), d.Owner, d.Reason, d.Rule
	case r.Category == match.CategoryUndetermined:
		f.Category = CategoryUndetermined
	default:
		f.Category, f.Rule = CategoryUnmanaged, ownership.RuleNoMatch
		f.Reason = "no state manages it and no rule matches"
	}
	return f
}

// Counts holds the number of findings per category.
type Counts map[Category]int

// Coverage returns the IaC coverage percentage: managed and double-managed resources over those
// plus unmanaged ones. aws-managed, derived, other-iac, ignored and undetermined resources don't
// count. It reports false when the denominator is zero.
func (c Counts) Coverage() (float64, bool) {
	inState := c[CategoryManaged] + c[CategoryDoubleManaged]
	total := inState + c[CategoryUnmanaged]
	if total == 0 {
		return 0, false
	}
	const percent = 100
	return float64(inState) * percent / float64(total), true
}

// Summary counts the findings of one account and region. Region is [inventory.GlobalColumn] for
// account-wide resources, and empty in an account's total.
type Summary struct {
	Account  string   `json:"account"`
	Region   string   `json:"region,omitempty"`
	Counts   Counts   `json:"counts"`
	Coverage *float64 `json:"coverage"` // percent; null when nothing is managed or unmanaged
}

// Summarize counts findings per account and region, sorted, and per account. It returns the
// region summaries, then the account summaries.
func Summarize(findings []Finding) ([]Summary, []Summary) {
	byRegion := map[[2]string]Counts{}
	byAccount := map[string]Counts{}
	for i := range findings {
		f := &findings[i]
		region := cmp.Or(f.Region, inventory.GlobalColumn)
		k := [2]string{f.Account, region}
		if byRegion[k] == nil {
			byRegion[k] = make(Counts)
		}
		if byAccount[f.Account] == nil {
			byAccount[f.Account] = make(Counts)
		}
		byRegion[k][f.Category]++
		byAccount[f.Account][f.Category]++
	}
	regions := make([]Summary, 0, len(byRegion))
	for k, c := range byRegion {
		regions = append(regions, summary(k[0], k[1], c))
	}
	slices.SortFunc(regions, func(a, b Summary) int {
		return cmp.Or(strings.Compare(a.Account, b.Account), compareRegion(a.Region, b.Region))
	})
	accounts := make([]Summary, 0, len(byAccount))
	for a, c := range byAccount {
		accounts = append(accounts, summary(a, "", c))
	}
	slices.SortFunc(accounts, func(a, b Summary) int { return strings.Compare(a.Account, b.Account) })
	return regions, accounts
}

// compareRegion sorts the global column first.
func compareRegion(a, b string) int {
	return cmp.Or(cmp.Compare(boolInt(a != inventory.GlobalColumn), boolInt(b != inventory.GlobalColumn)),
		strings.Compare(a, b))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func summary(account, region string, c Counts) Summary {
	full := make(Counts, len(Categories()))
	for _, cat := range Categories() {
		full[cat] = c[cat]
	}
	s := Summary{Account: account, Region: region, Counts: full}
	if pct, ok := full.Coverage(); ok {
		s.Coverage = &pct
	}
	return s
}

// TypeMatcher reports whether a finding's type matches one of the --types globs, which name
// CloudFormation or TF types.
type TypeMatcher []string

// Match reports whether a resource has a CloudFormation, source or TF type that a glob matches. An empty
// matcher matches everything.
func (tm TypeMatcher) Match(cfn, source string, tfTypes []string) bool {
	if len(tm) == 0 {
		return true
	}
	names := append([]string{cfn, source}, tfTypes...)
	for _, g := range tm {
		for _, n := range names {
			if n == "" {
				continue
			}
			// path.Match treats only "/" as a separator, which type names don't contain.
			if ok, err := path.Match(g, n); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// CFNTypes returns the curated CloudFormation types that a glob matches by their own name or one
// of their TF types, sorted.
func (tm TypeMatcher) CFNTypes(m *typemap.Map) []string {
	var out []string
	for _, t := range m.Curated() {
		for _, c := range t.CFN {
			if tm.Match(c, "", []string{t.TF}) && !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Gate counts the findings that --fail-on catches: findings in one of the categories whose type
// matches types.
func Gate(findings []Finding, failOn []Category, types TypeMatcher) int {
	n := 0
	for i := range findings {
		f := &findings[i]
		if slices.Contains(failOn, f.Category) && types.Match(f.CFNType, f.SourceType, f.TFTypes) {
			n++
		}
	}
	return n
}
