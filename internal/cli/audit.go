package cli

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/sarif"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	flagDiscover        = "discover"
	flagDiscoverPattern = "discover-pattern"
	flagFailOn          = "fail-on"
	infoURI             = "https://github.com/sassdavid/tfcensus"
)

func newAuditCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Audit state backends",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAuditBackendCmd(d, g))
	return cmd
}

type auditArgs struct {
	filters, paths []string
	concurrency    int
	discover       bool
	failOn         audit.Severity
}

func newAuditBackendCmd(d *app.Deps, g *globals) *cobra.Command {
	var (
		a      auditArgs
		failOn string
	)
	cmd := &cobra.Command{
		Use:   "backend [paths...]",
		Short: "Check state buckets, lock tables and the fleet of states",
		Long: `audit backend discovers units the way units does, reads their state the way inventory
does, and checks:

  - every s3 bucket the units' backends name (versioning, encryption and key policy, public
    access block, TLS-only bucket policy, object ownership, replication, lifecycle, logging),
  - every DynamoDB lock table (point-in-time recovery, KMS, stale locks, orphaned digests),
  - the fleet: states no unit resolves to, units with no state, empty states, copied lineages,
    double-managed resources, old terraform and provider versions, large states, and attribute
    names that look like secrets.

Each finding names its check (rule), severity, target and reason. Output never contains attribute
values: FL-SECRET lists attribute names only. Every call is a read. audit takes no lock.

--discover also lists the buckets of each account (s3:ListAllMyBuckets) and audits those matching
--discover-pattern that no unit names; their state keys show up as FL-ORPHAN.

Suppress accepted findings in config with audit.suppress entries: check, bucket, unit and key
are globs, reason is required, and after expires (YYYY-MM-DD) the entry stops matching and a
warning names it.

The exit code is 3 when an unsuppressed finding is at or above --fail-on, 1 when a check couldn't
run, and 4 when every failure was a credential problem.`,
		Example: `  tfcensus audit backend ~/src/infra/acme-live
  tfcensus audit backend --fail-on high --format sarif > audit.sarif
  tfcensus audit backend --profile 'acme-*' --discover --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.outFormat.Check(
				render.FormatTTY, render.FormatPlain, render.FormatMD, render.FormatJSON, render.FormatCSV,
				render.FormatSARIF,
			); err != nil {
				return usageError(err)
			}
			if a.concurrency < 1 {
				return usageError(fmt.Errorf("--%s must be at least 1, got %d", flagConcurrency, a.concurrency))
			}
			if failOn != "" {
				sev, err := audit.ParseSeverity(failOn)
				if err != nil {
					return usageError(fmt.Errorf("--%s: %w", flagFailOn, err))
				}
				a.failOn = sev
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			a.paths = args
			return runAudit(cmd, d, g.outFormat, &res.Config, a)
		},
	}
	addDiscoveryFlags(cmd, &a.filters)
	addAccountFlags(cmd)
	f := cmd.Flags()
	f.IntVar(&a.concurrency, flagConcurrency, tfstate.DefaultConcurrency, "states read at the same time")
	f.BoolVar(&a.discover, flagDiscover, false,
		"also audit buckets of each account that match --discover-pattern and that no unit names")
	f.StringArray(flagDiscoverPattern, nil,
		"bucket name glob for --discover (repeatable; config audit.discover_patterns)")
	f.StringVar(&failOn, flagFailOn, "", "exit 3 when a finding is at least this severe: info, low, medium or high")
	return cmd
}

func runAudit(cmd *cobra.Command, d *app.Deps, format render.Format, cfg *config.Config, a auditArgs) error {
	ctx := cmd.Context()
	suppress, err := audit.NewSuppressions(cfg.Audit.Suppress)
	if err != nil {
		return usageError(err)
	}
	loader := newLoader(d)
	disc, err := discoverUnits(cmd, d, cfg, a.filters, a.paths, loader)
	if err != nil {
		return err
	}
	var accts *accounts.Result
	if len(cfg.Accounts.Profiles) > 0 || cfg.Accounts.Org.Enabled {
		if accts, err = resolveAccounts(cmd, d, loader, cfg); err != nil {
			return err
		}
	}
	cwd, err := d.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	backends := newBackends(loader, accts, cfg, d)
	reader := tfstate.NewReader(tfstate.Options{
		Client: stateClient(backends), WorkDir: cwd, Concurrency: a.concurrency,
		TFPath: cfg.Binaries.Terraform, Logger: d.Logger,
	})
	states := reader.Read(ctx, disc.Units)
	opts := audit.Options{
		Credentials: backends.Credentials, S3: backends.S3, Policy: cfg.Audit.Policy, Suppress: suppress,
		Now: d.Clock(), Concurrency: a.concurrency, Logger: d.Logger,
	}
	if a.discover {
		opts.Discover = cfg.Audit.DiscoverPatterns
		if accts != nil {
			opts.Accounts = accts.Accounts
		}
	}
	m, err := typemap.Load()
	if err != nil {
		return err
	}
	rep, err := audit.Run(ctx, opts, audit.Input{Units: disc.Units, States: states, Index: match.Index(m, states)})
	if err != nil {
		return fmt.Errorf("audit backends: %w", err)
	}
	for _, w := range rep.Warnings {
		d.Logger.WarnContext(ctx, w)
	}
	if err = writeAudit(d, format, rep, disc, states); err != nil {
		return err
	}
	if err = errors.Join(discoveryError(disc), inventoryError(states), incompleteError(rep)); err != nil {
		return err
	}
	return failOnError(rep, a.failOn)
}

// incompleteError is the error for checks that could not run: exit 4 when every one failed on
// credentials, else exit 1.
func incompleteError(rep *audit.Report) error {
	if len(rep.Incomplete) == 0 {
		return nil
	}
	auth := 0
	for _, p := range rep.Incomplete {
		if errors.Is(p.Err, accounts.ErrAuth) || accounts.IsAccessDenied(p.Err) {
			auth++
		}
	}
	err := fmt.Errorf("audit: %d check(s) could not run (see the notes above)", len(rep.Incomplete))
	if auth == len(rep.Incomplete) {
		return &exitError{code: ExitAuth, err: err}
	}
	return err
}

func failOnError(rep *audit.Report, failOn audit.Severity) error {
	if failOn == "" {
		return nil
	}
	n := 0
	for _, f := range rep.Findings {
		if f.Severity.Rank() >= failOn.Rank() {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return &exitError{code: ExitFindings, err: fmt.Errorf("%d finding(s) at or above %s", n, failOn)}
}

func writeAudit(
	d *app.Deps,
	format render.Format,
	rep *audit.Report,
	disc discovery.Result,
	states []tfstate.UnitState,
) error {
	switch format {
	case render.FormatJSON:
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), rep)
		for _, e := range disc.Errors {
			env.Errors = append(env.Errors, render.Error{Target: e.Repo, Message: e.Err.Error()})
		}
		for i := range states {
			if u := &states[i]; u.Error != "" {
				env.Errors = append(env.Errors, render.Error{Target: stateLabel(u), Message: u.Error})
			}
		}
		for _, p := range rep.Incomplete {
			env.Errors = append(env.Errors, render.Error{Target: p.Target, Message: problemText(p)})
		}
		return render.JSON(d.Stdout, env)
	case render.FormatSARIF:
		return sarif.Write(d.Stdout, auditSARIF(rep))
	case render.FormatCSV:
		return writeAuditCSV(d.Stdout, rep)
	case render.FormatMD:
		return writeAuditMarkdown(d.Stdout, rep)
	case render.FormatAuto, render.FormatTTY, render.FormatPlain, render.FormatGitHub:
	}
	return writeAuditTable(d.Stdout, rep)
}

func problemText(p audit.Problem) string {
	if p.Check == "" {
		return p.Message
	}
	return p.Check + ": " + p.Message
}

func sarifLevel(s audit.Severity) string {
	switch s {
	case audit.SeverityHigh:
		return sarif.LevelError
	case audit.SeverityMedium:
		return sarif.LevelWarning
	case audit.SeverityLow, audit.SeverityInfo:
	}
	return sarif.LevelNote
}

// auditSARIF turns the report into a SARIF log. A unit finding points at the unit directory; a
// bucket or table finding at its s3:// or dynamodb:// URI. Suppressed findings carry an external
// suppression with the configured reason.
func auditSARIF(rep *audit.Report) *sarif.Log {
	l := sarif.New(render.ToolName, buildinfo.Get().Version, infoURI)
	run := &l.Runs[0]
	index := map[string]int{}
	for i, c := range rep.Checks {
		index[c.ID] = i
		run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarif.Rule{
			ID:                   c.ID,
			ShortDescription:     sarif.Message{Text: c.Title},
			Help:                 sarif.Message{Text: c.Remediation},
			DefaultConfiguration: sarif.Configuration{Level: sarifLevel(c.Severity)},
			Properties:           map[string]any{"severity": string(c.Severity)},
		})
	}
	add := func(f *audit.Finding) {
		uri := f.Target
		if !strings.Contains(uri, "://") && f.Unit != "" {
			uri = f.Unit
		}
		r := sarif.Result{
			RuleID:    f.Rule,
			RuleIndex: index[f.Rule],
			Level:     sarifLevel(f.Severity),
			Message:   sarif.Message{Text: f.Reason},
			Locations: []sarif.Location{
				{PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: uri}}},
			},
			PartialFingerprints: map[string]string{"tfcensusFinding/v1": f.Rule + "|" + f.Target + "|" + f.Key},
		}
		if f.Suppressed != nil {
			just := f.Suppressed.Reason
			if f.Suppressed.Expires != "" {
				just += " (until " + f.Suppressed.Expires + ")"
			}
			r.Suppressions = []sarif.Suppression{{Kind: "external", Status: "accepted", Justification: just}}
		}
		run.Results = append(run.Results, r)
	}
	for i := range rep.Findings {
		add(&rep.Findings[i])
	}
	for i := range rep.Suppressed {
		add(&rep.Suppressed[i])
	}
	return l
}

func findingColumns() []string { return []string{"SEVERITY", "CHECK", "TARGET", "REASON"} }

func findingRow(f *audit.Finding) []string {
	target := f.Target
	if f.Key != "" && !strings.HasSuffix(target, f.Key) && f.Table != "" {
		target += " " + f.Key
	}
	return []string{string(f.Severity), f.Rule, target, f.Reason}
}

func bucketColumns() []string {
	return []string{
		"BUCKET",
		"ACCOUNT",
		colRegion,
		"SOURCE",
		"UNITS",
		"STATES",
		"VERSIONING",
		"ENCRYPTION",
		"REPLICATION",
	}
}

func bucketRow(b *audit.Bucket) []string {
	repl := "none"
	if b.ReplicaOf != "" {
		repl = "replica of " + b.ReplicaOf
	}
	if len(b.Replication) > 0 {
		parts := make([]string, len(b.Replication))
		for i, r := range b.Replication {
			parts[i] = r.Bucket + " (" + dash(r.Region) + ", " + strings.ToLower(r.Status) + ")"
		}
		repl = strings.Join(parts, ", ")
	}
	return []string{
		b.Name, dash(b.Account), dash(b.Region), b.Source, strconv.Itoa(b.Units), strconv.Itoa(b.StateKeys),
		dash(b.Versioning), dash(b.Encryption), repl,
	}
}

func tableColumns() []string {
	return []string{"LOCK TABLE", colAccount, colRegion, "UNITS", "PITR", "ENCRYPTION", "LOCKS"}
}

func tableRow(t *audit.LockTable) []string {
	pitr := "-"
	if t.PITR != nil {
		pitr = map[bool]string{true: "on", false: "off"}[*t.PITR]
	}
	return []string{
		t.Name, dash(t.Account), dash(t.Region), strconv.Itoa(t.Units), pitr, dash(t.Encryption),
		strconv.Itoa(t.Locks),
	}
}

// auditNotes lists suppressed findings, warnings and checks that couldn't run, one line each.
func auditNotes(rep *audit.Report) []string {
	var notes []string
	for _, f := range rep.Suppressed {
		until := ""
		if f.Suppressed.Expires != "" {
			until = ", until " + f.Suppressed.Expires
		}
		notes = append(notes, fmt.Sprintf("suppressed %s %s (%s%s)", f.Rule, f.Target, f.Suppressed.Reason, until))
	}
	for _, w := range rep.Warnings {
		notes = append(notes, "warning: "+w)
	}
	for _, p := range rep.Incomplete {
		notes = append(notes, p.Target+": could not check: "+problemText(p))
	}
	return notes
}

func auditSummary(rep *audit.Report) string {
	counts := make(map[audit.Severity]int)
	for _, f := range rep.Findings {
		counts[f.Severity]++
	}
	var parts []string
	for _, s := range []audit.Severity{audit.SeverityHigh, audit.SeverityMedium, audit.SeverityLow, audit.SeverityInfo} {
		parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
	}
	return fmt.Sprintf("%d findings (%s), %d suppressed; %d buckets, %d lock tables; %d checks could not run",
		len(rep.Findings), strings.Join(parts, ", "), len(rep.Suppressed), len(rep.Buckets), len(rep.LockTables),
		len(rep.Incomplete))
}

func writeTab(w io.Writer, cols []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
	_, _ = fmt.Fprintln(tw, strings.Join(cols, "\t"))
	for _, r := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
}

type auditSection struct {
	cols []string
	rows [][]string
}

func auditSections(rep *audit.Report) []auditSection {
	var findings, buckets, tables [][]string
	for i := range rep.Findings {
		findings = append(findings, findingRow(&rep.Findings[i]))
	}
	for i := range rep.Buckets {
		buckets = append(buckets, bucketRow(&rep.Buckets[i]))
	}
	for i := range rep.LockTables {
		tables = append(tables, tableRow(&rep.LockTables[i]))
	}
	sections := []auditSection{{findingColumns(), findings}, {bucketColumns(), buckets}}
	if len(tables) > 0 {
		sections = append(sections, auditSection{tableColumns(), tables})
	}
	return sections
}

func writeAuditTable(w io.Writer, rep *audit.Report) error {
	for i, s := range auditSections(rep) {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		if err := writeTab(w, s.cols, s.rows); err != nil {
			return err
		}
	}
	if notes := auditNotes(rep); len(notes) > 0 {
		_, _ = fmt.Fprintln(w)
		for _, n := range notes {
			_, _ = fmt.Fprintln(w, n)
		}
	}
	_, _ = fmt.Fprintln(w)
	if _, err := fmt.Fprintln(w, auditSummary(rep)); err != nil {
		return fmt.Errorf("write audit: %w", err)
	}
	return nil
}

func writeAuditMarkdown(w io.Writer, rep *audit.Report) error {
	var b strings.Builder
	for i, s := range auditSections(rep) {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("| " + strings.Join(s.cols, " | ") + " |\n")
		b.WriteString(strings.Repeat("| --- ", len(s.cols)) + "|\n")
		for _, row := range s.rows {
			for j, cell := range row {
				row[j] = strings.ReplaceAll(cell, "|", `\|`)
			}
			b.WriteString("| " + strings.Join(row, " | ") + " |\n")
		}
	}
	if notes := auditNotes(rep); len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
	}
	b.WriteString("\n" + auditSummary(rep) + "\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write audit: %w", err)
	}
	return nil
}

func writeAuditCSV(w io.Writer, rep *audit.Report) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"rule", "severity", "target", "bucket", "table", "key", "unit", "account", "region", "reason",
		"suppressed", "suppression_reason", "expires",
	})
	write := func(f *audit.Finding) {
		suppressed, reason, expires := "false", "", ""
		if f.Suppressed != nil {
			suppressed, reason, expires = "true", f.Suppressed.Reason, f.Suppressed.Expires
		}
		_ = cw.Write([]string{
			f.Rule, string(f.Severity), f.Target, f.Bucket, f.Table, f.Key, f.Unit, f.Account, f.Region, f.Reason,
			suppressed, reason, expires,
		})
	}
	for i := range rep.Findings {
		write(&rep.Findings[i])
	}
	for i := range rep.Suppressed {
		write(&rep.Suppressed[i])
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("write audit: %w", err)
	}
	return nil
}
