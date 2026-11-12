package cli

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/sarif"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// ownerTags are the tags the report shows next to an unmanaged resource, when present.
func ownerTags() []string { return []string{"owner", "Owner", "created-by", "CreatedBy", "creator"} }

func writeScan(d *app.Deps, format render.Format, rep *scan.Report, problems []scan.Problem) error {
	now := d.Clock()
	switch format {
	case render.FormatJSON:
		env := render.NewEnvelope(buildinfo.Get().Version, now, rep)
		for _, p := range problems {
			env.Errors = append(env.Errors, render.Error{Target: p.Target, Message: p.Message})
		}
		return render.JSON(d.Stdout, env)
	case render.FormatSARIF:
		return sarif.Write(d.Stdout, scanSARIF(rep))
	case render.FormatCSV:
		return writeScanCSV(d.Stdout, rep)
	case render.FormatMD:
		return writeScanMarkdown(d.Stdout, rep, problems, now)
	case render.FormatAuto, render.FormatTTY, render.FormatPlain, render.FormatGitHub:
	}
	return writeScanText(d.Stdout, rep, problems, now)
}

func summaryColumns() []string {
	cols := []string{colAccount, colRegion}
	for _, c := range scan.Categories() {
		cols = append(cols, strings.ToUpper(string(c)))
	}
	return append(cols, "COVERAGE")
}

func summaryRow(s *scan.Summary) []string {
	region := cmp.Or(s.Region, "total")
	row := []string{s.Account, region}
	for _, c := range scan.Categories() {
		row = append(row, strconv.Itoa(s.Counts[c]))
	}
	return append(row, coverageCell(s.Coverage))
}

func coverageCell(pct *float64) string {
	if pct == nil {
		return "-"
	}
	return strconv.FormatFloat(*pct, 'f', 1, 64) + "%"
}

func summaryRows(rep *scan.Report) [][]string {
	var rows [][]string
	for _, acct := range rep.Accounts {
		for i := range rep.Regions {
			if rep.Regions[i].Account == acct.Account {
				rows = append(rows, summaryRow(&rep.Regions[i]))
			}
		}
		rows = append(rows, summaryRow(&acct))
	}
	return rows
}

// age renders how long ago an RFC 3339 time was, in days.
func age(created string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return ""
	}
	const day = 24 * time.Hour
	return strconv.Itoa(int(now.Sub(t)/day)) + "d"
}

// findingDetail is the name, key, owner tags and age of an unmanaged finding.
func findingDetail(f *scan.Finding, now time.Time) []string {
	var tags []string
	for _, k := range ownerTags() {
		if v, ok := f.Tags[k]; ok {
			tags = append(tags, k+"="+v)
		}
	}
	return []string{
		cmp.Or(f.Name, "-"),
		f.Key(),
		cmp.Or(strings.Join(tags, " "), "-"),
		cmp.Or(age(f.Created, now), "-"),
	}
}

// byType groups findings of one category by type, in report order.
func byType(rep *scan.Report, c scan.Category) ([]string, map[string][]*scan.Finding) {
	groups := map[string][]*scan.Finding{}
	var order []string
	for i := range rep.Findings {
		f := &rep.Findings[i]
		if f.Category != c {
			continue
		}
		if _, ok := groups[f.Type()]; !ok {
			order = append(order, f.Type())
		}
		groups[f.Type()] = append(groups[f.Type()], f)
	}
	slices.Sort(order)
	return order, groups
}

func refsText(refs []tfstate.Ref) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		label := r.Unit
		if r.Workspace != "" && r.Workspace != tfstate.DefaultWorkspace {
			label += ":" + r.Workspace
		}
		parts[i] = label + " (" + r.Address + ")"
	}
	return strings.Join(parts, " and ")
}

// undeterminedReasons counts undetermined findings per reason, sorted by reason.
func undeterminedReasons(rep *scan.Report) []string {
	counts := map[string]int{}
	for i := range rep.Findings {
		if f := &rep.Findings[i]; f.Category == scan.CategoryUndetermined {
			counts[f.Reason]++
		}
	}
	out := make([]string, 0, len(counts))
	for _, r := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, fmt.Sprintf("%s: %d", r, counts[r]))
	}
	return out
}

// scanNotes lists failed layers, problems and warnings, one line each.
func scanNotes(rep *scan.Report, problems []scan.Problem) []string {
	var notes []string
	for _, r := range rep.Runs {
		if r.Status == inventory.RunDenied || r.Status == inventory.RunError || r.Status == inventory.RunPartial {
			where := r.Account + " " + cmp.Or(r.Region, inventory.GlobalColumn)
			notes = append(notes, fmt.Sprintf("layer %s in %s: %s", r.Layer, where, r.Status))
		}
	}
	for _, p := range problems {
		notes = append(notes, p.Target+": error: "+p.Message)
	}
	for _, w := range rep.Warnings {
		notes = append(notes, "warning: "+w)
	}
	return notes
}

func scanSummary(rep *scan.Report) string {
	total := make(scan.Counts)
	for i := range rep.Findings {
		total[rep.Findings[i].Category]++
	}
	parts := make([]string, 0, len(scan.Categories()))
	for _, c := range scan.Categories() {
		parts = append(parts, fmt.Sprintf("%d %s", total[c], c))
	}
	pct := "-"
	if p, ok := total.Coverage(); ok {
		pct = coverageCell(&p)
	}
	line := fmt.Sprintf("%d resources: %s; coverage %s", len(rep.Findings), strings.Join(parts, ", "), pct)
	if rep.Cached {
		line += " (inventory from cache)"
	}
	return line
}

func writeScanText(w io.Writer, rep *scan.Report, problems []scan.Problem, now time.Time) error {
	if err := writeTab(w, summaryColumns(), summaryRows(rep)); err != nil {
		return err
	}
	if err := writeUnmanagedText(w, rep, now); err != nil {
		return err
	}
	if n := countOf(rep, scan.CategoryDoubleManaged); n > 0 {
		_, _ = fmt.Fprintf(w, "\nDouble-managed (%d)\n", n)
		for i := range rep.Findings {
			if f := &rep.Findings[i]; f.Category == scan.CategoryDoubleManaged {
				_, _ = fmt.Fprintf(w, "  %s %s: %s\n", f.Type(), f.Key(), refsText(f.ManagedBy))
			}
		}
	}
	if reasons := undeterminedReasons(rep); len(reasons) > 0 {
		_, _ = fmt.Fprintf(w, "\nUndetermined (%d)\n", countOf(rep, scan.CategoryUndetermined))
		for _, r := range reasons {
			_, _ = fmt.Fprintln(w, "  "+r)
		}
	}
	if notes := scanNotes(rep, problems); len(notes) > 0 {
		_, _ = fmt.Fprintln(w)
		for _, n := range notes {
			_, _ = fmt.Fprintln(w, n)
		}
	}
	_, _ = fmt.Fprintln(w)
	if _, err := fmt.Fprintln(w, scanSummary(rep)); err != nil {
		return fmt.Errorf("write scan: %w", err)
	}
	return nil
}

// writeUnmanagedText prints the unmanaged findings grouped by type.
func writeUnmanagedText(w io.Writer, rep *scan.Report, now time.Time) error {
	types, groups := byType(rep, scan.CategoryUnmanaged)
	if len(types) == 0 {
		return nil
	}
	_, _ = fmt.Fprintf(w, "\nUnmanaged (%d)\n", countOf(rep, scan.CategoryUnmanaged))
	for _, t := range types {
		_, _ = fmt.Fprintf(w, "%s (%d)\n", t, len(groups[t]))
		rows := make([][]string, 0, len(groups[t]))
		for _, f := range groups[t] {
			rows = append(rows, findingDetail(f, now))
		}
		if err := writeRows(w, rows); err != nil {
			return err
		}
	}
	return nil
}

// writeRows prints aligned rows without a header, indented by two spaces.
func writeRows(w io.Writer, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}
	var b strings.Builder
	if err := writeTab(&b, rows[0], rows[1:]); err != nil {
		return err
	}
	for line := range strings.Lines(b.String()) {
		if _, err := io.WriteString(w, "  "+line); err != nil {
			return fmt.Errorf("write scan: %w", err)
		}
	}
	return nil
}

func countOf(rep *scan.Report, c scan.Category) int {
	n := 0
	for i := range rep.Findings {
		if rep.Findings[i].Category == c {
			n++
		}
	}
	return n
}

func mdRow(cells []string) string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	return "| " + strings.Join(out, " | ") + " |\n"
}

func writeScanMarkdown(w io.Writer, rep *scan.Report, problems []scan.Problem, now time.Time) error {
	var b strings.Builder
	cols := summaryColumns()
	b.WriteString(mdRow(cols))
	b.WriteString(strings.Repeat("| --- ", len(cols)) + "|\n")
	for _, row := range summaryRows(rep) {
		b.WriteString(mdRow(row))
	}
	for _, acct := range rep.Accounts {
		b.WriteString("\n<details><summary>Account " + acct.Account + ": " +
			strconv.Itoa(acct.Counts[scan.CategoryUnmanaged]) + " unmanaged, " +
			strconv.Itoa(acct.Counts[scan.CategoryDoubleManaged]) + " double-managed</summary>\n\n")
		b.WriteString(mdRow([]string{"CATEGORY", "TYPE", "NAME", "ID", "TAGS", "AGE", "DETAIL"}))
		b.WriteString(strings.Repeat("| --- ", 7) + "|\n") //nolint:mnd // seven columns
		for i := range rep.Findings {
			f := &rep.Findings[i]
			if f.Account != acct.Account ||
				(f.Category != scan.CategoryUnmanaged && f.Category != scan.CategoryDoubleManaged) {
				continue
			}
			detail := findingDetail(f, now)
			extra := f.Reason
			if f.Category == scan.CategoryDoubleManaged {
				extra = refsText(f.ManagedBy)
			}
			b.WriteString(mdRow(append(append([]string{string(f.Category), f.Type()}, detail...), extra)))
		}
		b.WriteString("\n</details>\n")
	}
	if reasons := undeterminedReasons(rep); len(reasons) > 0 {
		b.WriteString("\nUndetermined:\n\n")
		for _, r := range reasons {
			b.WriteString("- " + r + "\n")
		}
	}
	if notes := scanNotes(rep, problems); len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
	}
	b.WriteString("\n" + scanSummary(rep) + "\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write scan: %w", err)
	}
	return nil
}

func writeScanCSV(w io.Writer, rep *scan.Report) error {
	cw := csv.NewWriter(w)
	_ = cw.Write(strings.Split("account,region,category,cfn_type,source_type,tf_types,arn,id,name,owner,rule,"+
		"reason,managed_by,created,sources,tags", ","))
	for i := range rep.Findings {
		f := &rep.Findings[i]
		managed := make([]string, len(f.ManagedBy))
		for j, r := range f.ManagedBy {
			managed[j] = r.Unit + ":" + r.Workspace + ":" + r.Address
		}
		tags := make([]string, 0, len(f.Tags))
		for _, k := range slices.Sorted(maps.Keys(f.Tags)) {
			tags = append(tags, k+"="+f.Tags[k])
		}
		_ = cw.Write([]string{
			f.Account, cmp.Or(f.Region, inventory.GlobalColumn), string(f.Category), f.CFNType, f.SourceType,
			strings.Join(f.TFTypes, ";"), f.ARN, f.ID, f.Name, f.Owner, f.Rule, f.Reason, strings.Join(managed, ";"),
			f.Created, strings.Join(f.Sources, ";"), strings.Join(tags, ";"),
		})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("write scan: %w", err)
	}
	return nil
}

// scanSARIF turns unmanaged and double-managed findings into SARIF results. A double-managed
// finding points at its first unit directory, an unmanaged one at aws://account/region/type/id.
func scanSARIF(rep *scan.Report) *sarif.Log {
	l := sarif.New(render.ToolName, buildinfo.Get().Version, infoURI)
	run := &l.Runs[0]
	run.Tool.Driver.Rules = []sarif.Rule{
		{
			ID:               string(scan.CategoryUnmanaged),
			ShortDescription: sarif.Message{Text: "AWS resource that no unit manages"},
			Help: sarif.Message{
				Text: "Import it into a unit (tfcensus codify), or add an ownership rule with a reason.",
			},
			DefaultConfiguration: sarif.Configuration{Level: sarif.LevelWarning},
		},
		{
			ID:               string(scan.CategoryDoubleManaged),
			ShortDescription: sarif.Message{Text: "AWS resource that more than one state manages"},
			Help: sarif.Message{
				Text: "Remove it from every state but one (terraform state rm, or a removed block).",
			},
			DefaultConfiguration: sarif.Configuration{Level: sarif.LevelError},
		},
	}
	for i := range rep.Findings {
		f := &rep.Findings[i]
		idx := 0
		uri := "aws://" + f.Account + "/" + cmp.Or(f.Region, inventory.GlobalColumn) + "/" + f.Type() + "/" +
			cmp.Or(f.ID, f.ARN)
		level := sarif.LevelWarning
		switch f.Category {
		case scan.CategoryUnmanaged:
		case scan.CategoryDoubleManaged:
			idx, level = 1, sarif.LevelError
			if len(f.ManagedBy) > 0 {
				uri = f.ManagedBy[0].Unit
			}
		case scan.CategoryManaged, scan.CategoryOtherIaC, scan.CategoryAWSManaged, scan.CategoryDerived,
			scan.CategoryIgnored, scan.CategoryUndetermined:
			continue
		}
		msg := f.Type() + " " + f.Key() + ": " + f.Reason
		if f.Category == scan.CategoryDoubleManaged {
			msg = f.Type() + " " + f.Key() + " is managed by " + refsText(f.ManagedBy)
		}
		run.Results = append(run.Results, sarif.Result{
			RuleID: string(f.Category), RuleIndex: idx, Level: level, Message: sarif.Message{Text: msg},
			Locations: []sarif.Location{
				{PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: uri}}},
			},
			PartialFingerprints: map[string]string{fingerprintV1: string(f.Category) + "|" + f.Key()},
		})
	}
	return l
}
