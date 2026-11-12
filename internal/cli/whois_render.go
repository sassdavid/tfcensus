package cli

import (
	"cmp"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/sarif"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/whois"
)

// whoisReport is the data of whois --format json.
type whoisReport struct {
	Answers  []whois.Answer `json:"answers"`
	Warnings []string       `json:"warnings"`
}

func writeWhois(d *app.Deps, format render.Format, rep *whoisReport, problems []scan.Problem) error {
	switch format {
	case render.FormatJSON:
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), rep)
		for _, p := range problems {
			env.Errors = append(env.Errors, render.Error{Target: p.Target, Message: p.Message})
		}
		return render.JSON(d.Stdout, env)
	case render.FormatSARIF:
		findings := make([]scan.Finding, len(rep.Answers))
		for i := range rep.Answers {
			findings[i] = rep.Answers[i].Finding
		}
		return sarif.Write(d.Stdout, scanSARIF(&scan.Report{Findings: findings}))
	case render.FormatMD:
		return writeWhoisMarkdown(d.Stdout, rep, problems)
	case render.FormatAuto, render.FormatTTY, render.FormatPlain, render.FormatCSV, render.FormatGitHub:
	}
	return writeWhoisText(d.Stdout, rep, problems)
}

// answerHeadline is "managed by identity", "double-managed by arn" or the ownership category with
// its owner.
func answerHeadline(a *whois.Answer) string {
	f := &a.Finding
	switch {
	case len(a.ManagedBy) > 0 && a.Key != "":
		return string(f.Category) + " by " + string(a.Key)
	case f.Owner != "":
		return string(f.Category) + " (owner " + f.Owner + ")"
	}
	return string(f.Category)
}

func managerRow(m *whois.Manager) []string {
	unit := m.Unit
	if m.Workspace != "" && m.Workspace != "default" {
		unit += ":" + m.Workspace
	}
	return []string{unit, m.Address, dash(m.Account), cmp.Or(m.Region, inventory.GlobalColumn)}
}

// answerDetail is the reason and rule, and the live lookup when one ran or was skipped.
func answerDetail(a *whois.Answer) string {
	out := a.Finding.Reason + " (" + a.Finding.Rule + ")"
	switch a.Live {
	case whois.LiveNotNeeded:
		return out
	case whois.LiveOffline:
		return out + "; tags not read (offline)"
	case whois.LiveFound, whois.LiveNotFound, whois.LiveNotListed, whois.LiveNoCredentials:
	}
	return out + "; live: " + string(a.Live)
}

func writeWhoisText(w io.Writer, rep *whoisReport, problems []scan.Problem) error {
	for i := range rep.Answers {
		a := &rep.Answers[i]
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintf(w, "%s: %s\n", a.Query, answerHeadline(a))
		if a.Finding.Category == scan.CategoryDoubleManaged {
			_, _ = fmt.Fprintf(w, "  warning: %d states manage it; keep it in one\n", len(a.ManagedBy))
		}
		if len(a.ManagedBy) > 0 {
			rows := make([][]string, 0, len(a.ManagedBy))
			for j := range a.ManagedBy {
				rows = append(rows, managerRow(&a.ManagedBy[j]))
			}
			if err := writeRows(w, rows); err != nil {
				return err
			}
			if a.Key == "" {
				_, _ = fmt.Fprintln(w, "  "+answerDetail(a))
			}
			continue
		}
		_, _ = fmt.Fprintln(w, "  "+answerDetail(a))
	}
	if notes := whoisNotes(rep, problems); len(notes) > 0 {
		_, _ = fmt.Fprintln(w)
		for _, n := range notes {
			_, _ = fmt.Fprintln(w, n)
		}
	}
	_, _ = fmt.Fprintln(w)
	if _, err := fmt.Fprintln(w, whoisSummary(rep)); err != nil {
		return fmt.Errorf("write whois: %w", err)
	}
	return nil
}

func whoisNotes(rep *whoisReport, problems []scan.Problem) []string {
	var out []string
	for _, warn := range rep.Warnings {
		out = append(out, "warning: "+warn)
	}
	for _, p := range problems {
		out = append(out, "error: "+p.Target+": "+p.Message)
	}
	return out
}

func whoisSummary(rep *whoisReport) string {
	counts := make(map[scan.Category]int)
	for i := range rep.Answers {
		counts[rep.Answers[i].Finding.Category]++
	}
	var parts []string
	for _, c := range scan.Categories() {
		if counts[c] > 0 {
			parts = append(parts, strconv.Itoa(counts[c])+" "+string(c))
		}
	}
	return fmt.Sprintf("%d %s: %s", len(rep.Answers), plural(len(rep.Answers), "query", "queries"),
		strings.Join(parts, ", "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func writeWhoisMarkdown(w io.Writer, rep *whoisReport, problems []scan.Problem) error {
	var b strings.Builder
	cols := []string{"QUERY", "CATEGORY", "KEY", "UNIT", "ADDRESS", "ACCOUNT", "REGION", "DETAIL"}
	b.WriteString(mdRow(cols))
	b.WriteString(strings.Repeat("| --- ", len(cols)) + "|\n")
	for i := range rep.Answers {
		a := &rep.Answers[i]
		head := []string{a.Query, string(a.Finding.Category), dash(string(a.Key))}
		if len(a.ManagedBy) == 0 {
			region := cmp.Or(a.Finding.Region, inventory.GlobalColumn)
			b.WriteString(mdRow(append(head, "-", "-", dash(a.Finding.Account), region, answerDetail(a))))
			continue
		}
		for j := range a.ManagedBy {
			b.WriteString(mdRow(append(head, append(managerRow(&a.ManagedBy[j]), a.Finding.Reason)...)))
		}
	}
	if notes := whoisNotes(rep, problems); len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
	}
	b.WriteString("\n" + whoisSummary(rep) + "\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write whois: %w", err)
	}
	return nil
}
