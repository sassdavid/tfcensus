package htmlreport

import (
	"bytes"
	"cmp"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/report"
	"github.com/sassdavid/tfcensus/internal/scan"
)

var (
	//go:embed assets/report.css
	css string
	//go:embed assets/report.js
	js string
	//go:embed assets/report.html.tmpl
	page string
)

// ErrNoReports is returned when no input is a scan, drift or audit report.
var ErrNoReports = errors.New("no scan, drift or audit report to render")

// maxSeries is the most accounts the coverage trend draws lines for, besides the total.
const maxSeries = 7

func palette() []string {
	return []string{"#3b5bdb", "#c2410c", "#2f7d32", "#9c36b5", "#0b7285", "#e8590c", "#5c940d", "#d6336c"}
}

// Render writes the HTML report. The newest scan fills the scan section and every scan becomes a
// point of the coverage trend; the newest drift and audit reports fill theirs. The output depends
// only on the inputs, so the same files give the same bytes.
func Render(w io.Writer, saved []*report.Saved, version string) error {
	v := &view{CSS: template.CSS(css), JS: template.JS(js), Version: version} //nolint:gosec // embedded, trusted assets
	scans, err := v.sort(saved)
	if err != nil {
		return err
	}
	if len(scans) > 0 {
		v.Scan = scanSection(scans)
	}
	if v.drift != nil {
		v.Drift = driftSection(v.drift)
	}
	if v.audit != nil {
		v.Audit = auditSection(v.audit)
	}
	t, err := template.New("report").Parse(page)
	if err != nil {
		return fmt.Errorf("parse report template: %w", err)
	}
	var buf bytes.Buffer
	if err = t.Execute(&buf, v); err != nil {
		return fmt.Errorf("render report: %w", err)
	}
	if _, err = w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// sort picks the newest drift and audit reports, lists the sources, and returns the scans oldest
// first.
func (v *view) sort(saved []*report.Saved) ([]*report.Saved, error) {
	var scans []*report.Saved
	var newest time.Time
	for _, s := range saved {
		switch s.Kind {
		case report.KindScan:
			scans = append(scans, s)
		case report.KindDrift:
			if v.drift == nil || s.GeneratedAt.After(v.drift.GeneratedAt) {
				v.drift = s
			}
		case report.KindAudit:
			if v.audit == nil || s.GeneratedAt.After(v.audit.GeneratedAt) {
				v.audit = s
			}
		case report.KindIndex:
			return nil, fmt.Errorf("%s is a state index; report html reads scan, drift and audit JSON", s.Path)
		}
		if s.GeneratedAt.After(newest) {
			newest = s.GeneratedAt
		}
		v.Sources = append(
			v.Sources,
			source{Name: filepath.Base(s.Path), Kind: string(s.Kind), Generated: stamp(s.GeneratedAt)},
		)
	}
	if len(scans) == 0 && v.drift == nil && v.audit == nil {
		return nil, ErrNoReports
	}
	slices.SortStableFunc(v.Sources, func(a, b source) int {
		return cmp.Or(
			strings.Compare(a.Generated, b.Generated),
			strings.Compare(a.Kind, b.Kind),
			strings.Compare(a.Name, b.Name),
		)
	})
	slices.SortStableFunc(scans, func(a, b *report.Saved) int { return a.GeneratedAt.Compare(b.GeneratedAt) })
	v.Generated = stamp(newest)
	return scans, nil
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

type view struct {
	CSS       template.CSS
	JS        template.JS
	Version   string
	Generated string
	Sources   []source
	Scan      *scanView
	Drift     *driftView
	Audit     *auditView

	drift, audit *report.Saved
}

type source struct{ Name, Kind, Generated string }

// filter is one select above a table.
type filter struct {
	Key, Label string
	Values     []string
}

func values(set map[string]bool) []string { return slices.Sorted(maps.Keys(set)) }

type accountRow struct {
	Account  string
	Counts   []int
	Coverage string
}

type findingRow struct {
	Account, Region, Category, Type, Key, Name, Owner, Rule, Reason, ManagedBy string
}

type scanView struct {
	Generated  string
	Cached     bool
	Categories []string
	Accounts   []accountRow
	Total      accountRow
	Trend      *trend
	Filters    []filter
	Findings   []findingRow
	Managed    int
	Warnings   []string
}

func coverage(pct *float64) string {
	if pct == nil {
		return "-"
	}
	return strconv.FormatFloat(*pct, 'f', 1, 64) + "%"
}

func scanSection(scans []*report.Saved) *scanView {
	cur := scans[len(scans)-1]
	rep := cur.Scan
	sv := &scanView{Generated: stamp(cur.GeneratedAt), Cached: rep.Cached, Warnings: rep.Warnings}
	for _, c := range scan.Categories() {
		sv.Categories = append(sv.Categories, string(c))
	}
	total := make(scan.Counts)
	for _, a := range rep.Accounts {
		row := accountRow{Account: a.Account, Coverage: coverage(a.Coverage)}
		for _, c := range scan.Categories() {
			row.Counts = append(row.Counts, a.Counts[c])
			total[c] += a.Counts[c]
		}
		sv.Accounts = append(sv.Accounts, row)
	}
	sv.Total = accountRow{Account: totalSeries}
	for _, c := range scan.Categories() {
		sv.Total.Counts = append(sv.Total.Counts, total[c])
	}
	if pct, ok := total.Coverage(); ok {
		sv.Total.Coverage = coverage(&pct)
	} else {
		sv.Total.Coverage = "-"
	}
	accts, cats, types := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range rep.Findings {
		f := &rep.Findings[i]
		if f.Category == scan.CategoryManaged {
			sv.Managed++
			continue
		}
		var refs []string
		for _, r := range f.ManagedBy {
			refs = append(refs, r.Unit+" "+r.Address)
		}
		sv.Findings = append(sv.Findings, findingRow{
			Account:   f.Account,
			Region:    cmp.Or(f.Region, "global"),
			Category:  string(f.Category),
			Type:      f.Type(),
			Key:       f.Key(),
			Name:      f.Name,
			Owner:     f.Owner,
			Rule:      f.Rule,
			Reason:    f.Reason,
			ManagedBy: strings.Join(refs, ", "),
		})
		accts[f.Account], cats[string(f.Category)], types[f.Type()] = true, true, true
	}
	sv.Filters = []filter{
		{Key: "account", Label: "Account", Values: values(accts)},
		{Key: "category", Label: "Category", Values: values(cats)},
		{Key: "type", Label: "Type", Values: values(types)},
	}
	if len(scans) > 1 {
		sv.Trend = buildTrend(scans)
	}
	return sv
}

type driftChange struct {
	Unit, Address, Type, Action, Attributes string
}

type driftView struct {
	Generated string
	Mode      string
	Summary   drift.Summary
	Units     []drift.Unit
	Changes   []driftChange
	Filters   []filter
	Warnings  []string
}

func driftSection(s *report.Saved) *driftView {
	rep := s.Drift
	dv := &driftView{
		Generated: stamp(s.GeneratedAt),
		Mode:      string(rep.Mode),
		Summary:   rep.Summary,
		Warnings:  rep.Warnings,
	}
	units, statuses := map[string]bool{}, map[string]bool{}
	for _, u := range rep.Units {
		if u.Status == drift.StatusClean {
			continue
		}
		dv.Units = append(dv.Units, u)
		statuses[string(u.Status)] = true
	}
	for _, c := range rep.VisibleChanges() {
		paths := make([]string, 0, len(c.Attributes))
		for _, a := range c.Visible() {
			paths = append(paths, a.Path)
		}
		action := string(c.Action)
		if c.Revert != "" {
			action += " (plan: " + string(c.Revert) + ")"
		}
		dv.Changes = append(dv.Changes, driftChange{
			Unit: c.Unit, Address: c.Address, Type: c.Type, Action: action, Attributes: strings.Join(paths, ", "),
		})
		units[c.Unit] = true
	}
	dv.Filters = []filter{{Key: "unit", Label: "Unit", Values: values(units)}}
	return dv
}

type auditView struct {
	Generated  string
	Counts     []sevCount
	Suppressed int
	Findings   []audit.Finding
	Filters    []filter
	Incomplete []audit.Problem
	Warnings   []string
}

type sevCount struct {
	Severity string
	Count    int
}

func auditSection(s *report.Saved) *auditView {
	sum := report.SummarizeAudit(s.Audit, report.AuditFilter{})
	av := &auditView{
		Generated: stamp(s.GeneratedAt), Suppressed: sum.Suppressed, Findings: sum.Findings,
		Incomplete: sum.Incomplete, Warnings: sum.Warnings,
	}
	for _, sev := range []audit.Severity{audit.SeverityHigh, audit.SeverityMedium, audit.SeverityLow, audit.SeverityInfo} {
		av.Counts = append(av.Counts, sevCount{Severity: string(sev), Count: sum.Counts[sev]})
	}
	sevs, accts, rules := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range sum.Findings {
		sevs[string(f.Severity)], accts[cmp.Or(f.Account, "-")], rules[f.Rule] = true, true, true
	}
	av.Filters = []filter{
		{Key: "severity", Label: "Severity", Values: values(sevs)},
		{Key: "account", Label: "Account", Values: values(accts)},
		{Key: "rule", Label: "Check", Values: values(rules)},
	}
	return av
}
