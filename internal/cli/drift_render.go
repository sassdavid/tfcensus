package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/sarif"
)

// mdCap is the most bytes drift --format md writes, so the report fits a PR comment or an issue.
const mdCap = 60 * 1024

// maxAnnotationMembers caps the units named in one annotation.
const maxAnnotationMembers = 10

func writeDrift(d *app.Deps, format render.Format, rep *drift.Report, problems []driftProblem) error {
	switch format {
	case render.FormatJSON:
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), rep)
		for _, n := range driftErrors(rep, problems) {
			env.Errors = append(env.Errors, render.Error{Target: n.Target, Message: n.Message})
		}
		return render.JSON(d.Stdout, env)
	case render.FormatSARIF:
		return sarif.Write(d.Stdout, driftSARIF(rep, problems))
	case render.FormatGitHub:
		return writeDriftGitHub(d.Stdout, rep, problems)
	case render.FormatMD:
		return writeDriftMarkdown(d.Stdout, rep, problems)
	case render.FormatAuto, render.FormatTTY, render.FormatPlain, render.FormatCSV:
	}
	return writeDriftText(d.Stdout, rep, problems)
}

// driftErrors lists the failed units, then the repos that failed.
func driftErrors(rep *drift.Report, problems []driftProblem) []driftProblem {
	var out []driftProblem
	for _, u := range rep.FailedUnits() {
		out = append(out, driftProblem{Target: u.Path, Message: "plan failed: " + u.Reason})
	}
	return append(out, problems...)
}

// unitFile is the file a drift annotation or SARIF result points at.
func unitFile(unit string) string { return strings.TrimPrefix(unit+"/terragrunt.hcl", "./") }

func groupHeading(g *drift.Group) string {
	s := fmt.Sprintf("%s %s in %d unit(s)", g.Type, g.Action, len(g.Members))
	if g.Revert != "" {
		s += ", the plan reverts it (" + string(g.Revert) + ")"
	}
	return s
}

// attrCells are the path, the change and, with values, before and after.
func attrCells(a *drift.Attr, withValues bool) []string {
	change := string(a.Kind)
	if a.Sensitive {
		change += ", sensitive"
	}
	if a.Unknown {
		change += ", known after apply"
	}
	if a.ForcesReplacement {
		change += ", forces replacement"
	}
	cells := []string{a.Path, change}
	if withValues {
		before, after := "(differs per unit)", "(differs per unit)"
		if a.Before != nil && a.After != nil {
			before, after = *a.Before, *a.After
		}
		cells = append(cells, before, after)
	}
	return cells
}

func attrPaths(attrs []drift.Attr) string {
	if len(attrs) == 0 {
		return "(whole resource)"
	}
	paths := make([]string, len(attrs))
	for i := range attrs {
		paths[i] = attrs[i].Path
	}
	return strings.Join(paths, ", ")
}

// protectedNotes maps unit and address to why a noise rule didn't hide the change.
func protectedNotes(rep *drift.Report) map[string]string {
	out := map[string]string{}
	for i := range rep.Changes {
		if c := &rep.Changes[i]; c.Protected != "" && c.Hidden == nil {
			out[c.Label()] = c.Protected
		}
	}
	return out
}

func unitRows(rep *drift.Report) ([]string, [][]string) {
	cols := []string{colUnit, colStatus, "DRIFTED", "HIDDEN"}
	full := rep.Mode == drift.ModeFull
	if full {
		cols = append(cols, "REVERTS", "PENDING")
	}
	cols = append(cols, "REASON")
	rows := make([][]string, 0, len(rep.Units))
	for _, u := range rep.Units {
		row := []string{u.Path, string(u.Status), strconv.Itoa(u.Drifted), strconv.Itoa(u.Hidden)}
		if full {
			row = append(row, strconv.Itoa(u.Reverts), strconv.Itoa(u.Pending))
		}
		rows = append(rows, append(row, cmpDash(u.Reason)))
	}
	return cols, rows
}

func cmpDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func driftNotes(rep *drift.Report, problems []driftProblem) []string {
	var notes []string
	for _, e := range driftErrors(rep, problems) {
		notes = append(notes, e.Target+": error: "+e.Message)
	}
	for _, w := range rep.Warnings {
		notes = append(notes, "warning: "+w)
	}
	return notes
}

func writeDriftText(w io.Writer, rep *drift.Report, problems []driftProblem) error {
	cols, rows := unitRows(rep)
	if err := writeTab(w, cols, rows); err != nil {
		return err
	}
	withValues := hasValues(rep)
	protected := protectedNotes(rep)
	if len(rep.Groups) > 0 {
		_, _ = fmt.Fprintf(w, "\nDrift (%d resources in %d groups)\n", rep.Summary.Resources, len(rep.Groups))
	}
	for i := range rep.Groups {
		g := &rep.Groups[i]
		_, _ = fmt.Fprintf(w, "%s  %s\n", g.ID, groupHeading(g))
		attrRows := make([][]string, 0, len(g.Attributes))
		for j := range g.Attributes {
			attrRows = append(attrRows, attrCells(&g.Attributes[j], withValues))
		}
		if err := writeRows(w, attrRows); err != nil {
			return err
		}
		memberRows := make([][]string, 0, len(g.Members))
		for _, m := range g.Members {
			row := []string{m.Unit, m.Address}
			if note := protected[m.Unit+" "+m.Address]; note != "" {
				row = append(row, "("+note+")")
			}
			memberRows = append(memberRows, row)
		}
		_, _ = fmt.Fprintln(w, "  members:")
		if err := writeRows(w, indentRows(memberRows)); err != nil {
			return err
		}
	}
	if notes := driftNotes(rep, problems); len(notes) > 0 {
		_, _ = fmt.Fprintln(w)
		for _, n := range notes {
			_, _ = fmt.Fprintln(w, n)
		}
	}
	if _, err := fmt.Fprintln(w, "\n"+rep.Summary.Text()); err != nil {
		return fmt.Errorf("write drift: %w", err)
	}
	return nil
}

// indentRows adds two more spaces in front of each row's first cell.
func indentRows(rows [][]string) [][]string {
	for _, r := range rows {
		r[0] = "  " + r[0]
	}
	return rows
}

func hasValues(rep *drift.Report) bool {
	for i := range rep.Changes {
		for _, a := range rep.Changes[i].Attributes {
			if a.Before != nil || a.After != nil {
				return true
			}
		}
	}
	return false
}

func mdCode(s string) string { return "`" + strings.ReplaceAll(s, "`", "'") + "`" }

func writeDriftMarkdown(w io.Writer, rep *drift.Report, problems []driftProblem) error {
	var head strings.Builder
	head.WriteString(rep.Summary.Text() + "\n")
	if errs := driftErrors(rep, problems); len(errs) > 0 {
		head.WriteString("\nErrors:\n\n")
		for _, e := range errs {
			head.WriteString("- " + mdCode(e.Target) + ": " + e.Message + "\n")
		}
	}
	if len(rep.Warnings) > 0 {
		head.WriteString("\nWarnings:\n\n")
		for _, warn := range rep.Warnings {
			head.WriteString("- " + warn + "\n")
		}
	}
	withValues := hasValues(rep)
	protected := protectedNotes(rep)
	body := head.String()
	const footerRoom = 512
	shown := 0
	for i := range rep.Groups {
		section := mdGroup(&rep.Groups[i], withValues, protected)
		if len(body)+len(section)+footerRoom > mdCap {
			break
		}
		body += section
		shown++
	}
	if shown < len(rep.Groups) {
		body += fmt.Sprintf("\nTruncated: %d of %d groups shown, to stay under %d KB. "+
			"Run tfcensus drift --format json for the full report.\n", shown, len(rep.Groups), mdCap/1024) //nolint:mnd // KiB
	}
	if _, err := io.WriteString(w, body); err != nil {
		return fmt.Errorf("write drift: %w", err)
	}
	return nil
}

func mdGroup(g *drift.Group, withValues bool, protected map[string]string) string {
	var b strings.Builder
	b.WriteString("\n<details><summary>" + g.ID + ": " + groupHeading(g) + "</summary>\n\n")
	if len(g.Attributes) > 0 {
		cols := []string{"ATTRIBUTE", "CHANGE"}
		if withValues {
			cols = append(cols, "BEFORE", "AFTER")
		}
		b.WriteString(mdRow(cols))
		b.WriteString(strings.Repeat("| --- ", len(cols)) + "|\n")
		for j := range g.Attributes {
			b.WriteString(mdRow(attrCells(&g.Attributes[j], withValues)))
		}
		b.WriteString("\n")
	}
	b.WriteString("Members:\n\n")
	for _, m := range g.Members {
		line := "- " + mdCode(m.Unit) + " " + mdCode(m.Address)
		if note := protected[m.Unit+" "+m.Address]; note != "" {
			line += " (" + note + ")"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n</details>\n")
	return b.String()
}

// ghEscape escapes a workflow command's message; ghProperty also escapes a property value.
func ghEscape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func ghProperty(s string) string {
	return strings.NewReplacer(":", "%3A", ",", "%2C").Replace(ghEscape(s))
}

func ghCommand(level, file, title, msg string) string {
	props := []string{}
	if file != "" {
		props = append(props, "file="+ghProperty(file))
	}
	props = append(props, "title="+ghProperty(title))
	return "::" + level + " " + strings.Join(props, ",") + "::" + ghEscape(msg) + "\n"
}

// writeDriftGitHub writes one error per failed unit or repo, one warning per group, one per report
// warning, and a closing notice with the summary.
func writeDriftGitHub(w io.Writer, rep *drift.Report, problems []driftProblem) error {
	var b strings.Builder
	for _, u := range rep.FailedUnits() {
		b.WriteString(ghCommand("error", unitFile(u.Path), "tfcensus drift: plan failed", u.Path+": "+u.Reason))
	}
	for _, p := range problems {
		b.WriteString(ghCommand("error", "", "tfcensus drift: "+p.Target, p.Message))
	}
	for i := range rep.Groups {
		g := &rep.Groups[i]
		names := make([]string, 0, min(len(g.Members), maxAnnotationMembers))
		for _, m := range g.Members[:min(len(g.Members), maxAnnotationMembers)] {
			names = append(names, m.Unit+" "+m.Address)
		}
		msg := strings.Join(names, "\n")
		if more := len(g.Members) - len(names); more > 0 {
			msg += fmt.Sprintf("\nand %d more", more)
		}
		msg += "\nAttributes: " + attrPaths(g.Attributes)
		b.WriteString(ghCommand("warning", unitFile(g.Members[0].Unit),
			"tfcensus drift "+g.ID+": "+groupHeading(g), msg))
	}
	for _, warn := range rep.Warnings {
		b.WriteString(ghCommand("warning", "", "tfcensus drift", warn))
	}
	b.WriteString(ghCommand("notice", "", "tfcensus drift", rep.Summary.Text()))
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write drift: %w", err)
	}
	return nil
}

// SARIF rule IDs for drift.
const (
	sarifDrift      = "drift"
	sarifPlanFailed = "plan-failed"
)

// driftSARIF has one result per change and per failed unit. Hidden changes are suppressed results
// with the noise rule's reason.
func driftSARIF(rep *drift.Report, problems []driftProblem) *sarif.Log {
	l := sarif.New(render.ToolName, buildinfo.Get().Version, infoURI)
	run := &l.Runs[0]
	run.Tool.Driver.Rules = []sarif.Rule{
		{
			ID:               sarifDrift,
			ShortDescription: sarif.Message{Text: "Resource changed outside terraform"},
			Help: sarif.Message{
				Text: "Change the code to match, apply to revert the change, or add a drift.noise rule with a reason.",
			},
			DefaultConfiguration: sarif.Configuration{Level: sarif.LevelWarning},
		},
		{
			ID:                   sarifPlanFailed,
			ShortDescription:     sarif.Message{Text: "The unit's plan failed, so its drift is unknown"},
			Help:                 sarif.Message{Text: "Run terragrunt plan in the unit and fix the error."},
			DefaultConfiguration: sarif.Configuration{Level: sarif.LevelError},
		},
	}
	loc := func(uri string) []sarif.Location {
		return []sarif.Location{
			{PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: uri}}},
		}
	}
	for i := range rep.Changes {
		c := &rep.Changes[i]
		msg := fmt.Sprintf("%s (%s) drifted: %s; attributes: %s", c.Address, c.Type, c.Action, attrPaths(c.Visible()))
		if c.Revert != "" {
			msg += "; the plan reverts it (" + string(c.Revert) + ")"
		}
		res := sarif.Result{
			RuleID:    sarifDrift,
			Level:     sarif.LevelWarning,
			Message:   sarif.Message{Text: msg},
			Locations: loc(unitFile(c.Unit)),
			PartialFingerprints: map[string]string{
				"tfcensusDrift/v1": c.Unit + "|" + c.Address + "|" + string(c.Action),
			},
		}
		if c.Group != "" {
			res.Properties = map[string]any{"group": c.Group}
		}
		if c.Hidden != nil {
			res.Message.Text = fmt.Sprintf("%s (%s) drifted: %s; attributes: %s", c.Address, c.Type, c.Action,
				attrPaths(c.Attributes))
			res.Suppressions = []sarif.Suppression{{
				Kind: "external", Status: "accepted", Justification: c.Hidden.Rule + ": " + c.Hidden.Reason,
			}}
		}
		run.Results = append(run.Results, res)
	}
	for _, e := range driftErrors(rep, problems) {
		run.Results = append(run.Results, sarif.Result{
			RuleID: sarifPlanFailed, RuleIndex: 1, Level: sarif.LevelError,
			Message: sarif.Message{Text: e.Target + ": " + e.Message}, Locations: loc(unitFile(e.Target)),
			PartialFingerprints: map[string]string{"tfcensusDrift/v1": "failed|" + e.Target},
		})
	}
	return l
}
