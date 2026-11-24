package scan

import (
	"fmt"
	"io"
	"strings"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/term"
)

// Report is the result of a scan.
type Report struct {
	Regions  []Summary       `json:"regions"`
	Accounts []Summary       `json:"accounts"`
	Findings []Finding       `json:"findings"`
	Runs     []inventory.Run `json:"runs"`
	// Aggregator is the outcome of the AWS Config aggregator queries, when the config layer used them.
	Aggregator *inventory.AggregatorRun `json:"aggregator,omitempty"`
	Coverage   *inventory.Coverage      `json:"coverage"` // which layer listed each type, per account and region
	Warnings   []string                 `json:"warnings"`
	Cached     bool                     `json:"cached"` // the inventory and state index came from the cache
}

// Explanation is the evaluation trace of one resource: its match, every rule's outcome, and the
// final finding.
type Explanation struct {
	Resource *ownership.Resource `json:"resource"`
	Match    MatchStep           `json:"match"`
	Trace    []ownership.Step    `json:"trace"` // empty when matching decided
	Finding  Finding             `json:"finding"`
}

// MatchStep is the matcher's verdict.
type MatchStep struct {
	Category match.Category `json:"category"`
	Reason   string         `json:"reason"`
	Rule     string         `json:"rule"`
}

// Explain evaluates results[i] with a full trace. The rules aren't evaluated when the match or a
// CloudFormation stack decided. The other results are classified too, because a child can
// inherit its parent's category.
func Explain(results []match.Result, i int, src Sources) Explanation {
	r := &results[i]
	in := ownership.FromCloud(&r.Resource, src.Map, src.Index)
	e := Explanation{
		Resource: in,
		Match:    MatchStep{Category: r.Category, Reason: r.Reason, Rule: r.Rule},
		Trace:    []ownership.Step{},
		Finding:  Classify(results, src)[i],
	}
	if e.Finding.Rule != RuleCloudFormation &&
		(r.Category == match.CategoryUnmatched || r.Category == match.CategoryUndetermined) {
		e.Trace = src.Engine.Evaluate(in, nil, true).Trace
	}
	return e
}

// Tone is the style the text formats show c in: unmanaged red, double-managed bold red,
// undetermined yellow, managed green, ignored faint, the rest plain.
func (c Category) Tone() term.Tone {
	switch c {
	case CategoryManaged:
		return term.Good
	case CategoryDoubleManaged:
		return term.Severe
	case CategoryUnmanaged:
		return term.Bad
	case CategoryUndetermined:
		return term.Warn
	case CategoryIgnored:
		return term.Muted
	case CategoryOtherIaC, CategoryAWSManaged, CategoryDerived:
	}
	return term.Plain
}

// WriteExplanation prints an explanation as aligned text, with the category in its [Category.Tone].
func WriteExplanation(w io.Writer, e *Explanation) error {
	r := e.Resource
	typ := r.Type
	if typ == "" {
		typ = r.SourceType
	}
	if len(r.TFTypes) > 0 {
		typ += " (" + strings.Join(r.TFTypes, ", ") + ")"
	}
	region := r.Region
	if region == "" {
		region = inventory.GlobalColumn
	}
	row := func(k, v string) []term.Cell { return []term.Cell{{Text: k}, {Text: v}} }
	rows := [][]term.Cell{
		row("resource", e.Finding.Key()),
		row("type", typ),
		row("where", r.Account+" "+region),
		row("sources", strings.Join(r.Sources, ", ")),
		row("match", fmt.Sprintf("%s: %s (%s)", e.Match.Category, e.Match.Reason, e.Match.Rule)),
	}
	if s := e.Finding.Stack; s != nil {
		rows = append(rows, row("stack", s.Text()))
	}
	switch {
	case e.Finding.Rule == RuleCloudFormation:
		rows = append(rows, row("rules", "not evaluated: a CloudFormation stack lists it"))
	case len(e.Trace) == 0:
		rows = append(rows, row("rules", "not evaluated: matching decided"))
	default:
		rows = append(rows, row("rules", "first match wins"))
		for _, s := range e.Trace {
			outcome := string(s.Outcome)
			if s.Detail != "" {
				outcome += ": " + s.Detail
			}
			rows = append(rows, row("  "+s.Rule, outcome))
		}
	}
	for p, lead := e.Finding.Parent, "parent"; p != nil; p, lead = p.Parent, "  parent" {
		rows = append(rows, row(lead, p.Text()))
	}
	f := &e.Finding
	verdict := fmt.Sprintf("%s: %s (%s)", f.Category, f.Reason, f.Rule)
	if f.Owner != "" {
		verdict = fmt.Sprintf("%s, owner %s: %s (%s)", f.Category, f.Owner, f.Reason, f.Rule)
	}
	rows = append(rows, []term.Cell{{Text: "category"}, {Text: verdict, Tone: f.Category.Tone()}})
	if _, err := io.WriteString(w, term.Table(nil, rows)); err != nil {
		return fmt.Errorf("write explanation: %w", err)
	}
	return nil
}
