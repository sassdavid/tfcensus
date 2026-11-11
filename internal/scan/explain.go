package scan

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Report is the result of a scan.
type Report struct {
	Regions  []Summary           `json:"regions"`
	Accounts []Summary           `json:"accounts"`
	Findings []Finding           `json:"findings"`
	Runs     []inventory.Run     `json:"runs"`
	Coverage *inventory.Coverage `json:"coverage"` // which layer listed each type, per account and region
	Warnings []string            `json:"warnings"`
	Cached   bool                `json:"cached"` // the inventory and state index came from the cache
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

// Explain evaluates one match result with a full trace.
func Explain(r *match.Result, eng *ownership.Engine, m *typemap.Map, ix *ownership.Index) Explanation {
	in := ownership.FromCloud(&r.Resource, m, ix)
	e := Explanation{
		Resource: in,
		Match:    MatchStep{Category: r.Category, Reason: r.Reason, Rule: r.Rule},
		Trace:    []ownership.Step{},
		Finding:  classify(r, eng, m, ix),
	}
	if r.Category == match.CategoryUnmatched || r.Category == match.CategoryUndetermined {
		e.Trace = eng.Evaluate(in, nil, true).Trace
	}
	return e
}

// WriteExplanation prints an explanation as aligned text.
func WriteExplanation(w io.Writer, e *Explanation) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
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
	lines := [][2]string{
		{"resource", e.Finding.Key()},
		{"type", typ},
		{"where", r.Account + " " + region},
		{"sources", strings.Join(r.Sources, ", ")},
		{"match", fmt.Sprintf("%s: %s (%s)", e.Match.Category, e.Match.Reason, e.Match.Rule)},
	}
	for _, l := range lines {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", l[0], l[1])
	}
	if len(e.Trace) == 0 {
		_, _ = fmt.Fprintf(tw, "rules\tnot evaluated: matching decided\n")
	} else {
		_, _ = fmt.Fprintf(tw, "rules\tfirst match wins\n")
		for _, s := range e.Trace {
			outcome := string(s.Outcome)
			if s.Detail != "" {
				outcome += ": " + s.Detail
			}
			_, _ = fmt.Fprintf(tw, "  %s\t%s\n", s.Rule, outcome)
		}
	}
	f := &e.Finding
	verdict := fmt.Sprintf("%s: %s (%s)", f.Category, f.Reason, f.Rule)
	if f.Owner != "" {
		verdict = fmt.Sprintf("%s, owner %s: %s (%s)", f.Category, f.Owner, f.Reason, f.Rule)
	}
	_, _ = fmt.Fprintf(tw, "category\t%s\n", verdict)
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write explanation: %w", err)
	}
	return nil
}
