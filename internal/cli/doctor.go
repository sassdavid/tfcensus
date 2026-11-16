package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/toolcheck"
)

const (
	probeConcurrency = 4
	colAccount       = "ACCOUNT"
	colRegion        = "REGION"
	colUnit          = "UNIT"
	colStatus        = "STATUS"
	cellDenied       = "denied"
	cellError        = "error"
	cellNotFound     = "not found"
)

func newDoctorCmd(d *app.Deps, g *globals) *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check tool versions, AWS credentials and the inventory layers of each account and region",
		Long: `doctor runs terragrunt --version and the --tf-path binary's version command, and compares
them with the minimums: terragrunt 1.0.0, terraform 1.5.0 and tofu 1.6.0. It lists the features
a found version is too old for. A tool that isn't installed is reported but doesn't fail the run.
With --decrypt-with-tofu (config state.decrypt_with_tofu), it also checks the --tofu-path binary.

doctor then loads the credentials of every selected account source and probes each account and
region for the inventory layers a scan can use:

  re      Resource Explorer: an index in the region and a view to search it with
  config  AWS Config: a recorder that is recording
  cc      Cloud Control: always available, but slow and throttled
  native  tfcensus's own enumerators: always available

Without --profile or --org, doctor uses the default AWS credentials (AWS_PROFILE, environment
credentials or an instance role). Without --region, it probes the regions that have a Resource
Explorer index and the profile's own region. --all-regions probes every region the account has
enabled (account:ListRegions). --account and --exclude-account keep or drop account IDs before
doctor assumes any role in them.

doctor is read-only: it never creates indexes, views or recorders. Its notes say what an admin
could turn on. Results are cached for 24 hours under $XDG_CACHE_HOME/tfcensus/probe; --refresh
probes again.

A source whose credentials fail, such as a profile with an expired SSO token, is skipped with a
warning. doctor exits 4 when every source fails, and 1 when a tool is older than its minimum.`,
		Example: `  tfcensus doctor --profile 'acme-*'
  tfcensus doctor --profile acme-nonprod --region eu-west-1 --region us-east-1
  tfcensus doctor --org --management-profile acme-mgmt --format json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := g.outFormat.Check(
				render.FormatTTY, render.FormatPlain, render.FormatMD, render.FormatJSON,
			); err != nil {
				return usageError(err)
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			if err = checkRegions(cmd, &res.Config); err != nil {
				return err
			}
			return runDoctor(cmd, d, g.outFormat, &res.Config, refresh)
		},
	}
	addAccountFlags(cmd, d)
	addStateFlags(cmd)
	addAccountFilterFlags(cmd)
	addAllRegionsFlags(cmd)
	cmd.Flags().BoolVar(&refresh, "refresh", false, "probe again even when the cached result is fresh")
	return cmd
}

type doctorData struct {
	Tools      []toolcheck.Tool  `json:"tools"`
	Sources    []doctorSource    `json:"sources"`
	Accounts   []*probe.Report   `json:"accounts"`
	Aggregator *doctorAggregator `json:"aggregator,omitempty"`
}

type doctorSource struct {
	Source  string        `json:"source"`
	Kind    accounts.Kind `json:"kind,omitempty"`
	Account string        `json:"account,omitempty"`
	Name    string        `json:"name,omitempty"`
	RoleARN string        `json:"role_arn,omitempty"`
	Status  string        `json:"status"` // ok or skipped
	Reason  string        `json:"reason,omitempty"`
}

// doctorAggregator is the AWS Config aggregator named in accounts.aggregator.
type doctorAggregator struct {
	Name    string            `json:"name"`
	Profile string            `json:"profile,omitempty"`
	Region  string            `json:"region,omitempty"`
	Status  probe.Status      `json:"status"`
	Error   string            `json:"error,omitempty"`
	Sources []probe.AggSource `json:"sources,omitempty"`
	Cached  bool              `json:"cached,omitempty"`
}

func runDoctor(cmd *cobra.Command, d *app.Deps, format render.Format, cfg *config.Config, refresh bool) error {
	ctx := cmd.Context()
	loader := newLoader(d)
	res, err := resolve(cmd, d, loader, targetSelection(cfg))
	if err != nil {
		return err
	}
	warnUnmatched(cmd, d, cfg, res)
	prober, err := newProber(d, refresh)
	if err != nil {
		return err
	}
	enabled, regionErrs := enabledRegions(cmd, cfg, res.Accounts)
	for _, id := range slices.Sorted(maps.Keys(regionErrs)) {
		d.Logger.WarnContext(ctx, "probing the default regions instead", "account", id, "error", regionErrs[id])
	}
	data := doctorData{
		Tools:    checkTools(cmd, d, cfg),
		Sources:  doctorSources(res),
		Accounts: make([]*probe.Report, len(res.Accounts)),
	}
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(probeConcurrency)
	for i, acct := range res.Accounts {
		regions, listed := enabled[acct.ID]
		if !listed {
			regions = cfg.Regions
		}
		eg.Go(func() error {
			rep, probeErr := prober.Probe(egctx, acct, regions)
			if probeErr == nil && cfg.AllRegions && !listed {
				rep.Notes = append(rep.Notes, "could not list the enabled regions (account:ListRegions); "+
					"probed the default regions instead")
			}
			data.Accounts[i] = rep
			return probeErr
		})
	}
	if err = eg.Wait(); err != nil {
		return fmt.Errorf("probe accounts: %w", err)
	}
	if agg := cfg.Accounts.Aggregator; agg.Name != "" {
		data.Aggregator = checkAggregator(cmd, loader, prober, agg)
	}
	if err = writeDoctor(d, format, data, res); err != nil {
		return err
	}
	if len(res.Accounts) == 0 && len(res.Errors) > 0 {
		return noAccountsError(res)
	}
	return tooOldError(data.Tools)
}

// checkTools finds the terragrunt and terraform or tofu versions, and the binaries.tofu version
// when state.decrypt_with_tofu is on. A missing binary is reported, not an error: plain Terraform
// repos don't need terragrunt.
func checkTools(cmd *cobra.Command, d *app.Deps, cfg *config.Config) []toolcheck.Tool {
	runner := execx.NewRunner(d.Logger, execx.WithEnviron(func() []string { return d.Env }))
	tools := toolcheck.Check(cmd.Context(), runner, cfg.Binaries.Terragrunt, cfg.Binaries.Terraform)
	if cfg.State.DecryptWithTofu && cfg.Binaries.Tofu != cfg.Binaries.Terraform {
		tools = append(tools, toolcheck.CheckTofu(cmd.Context(), runner, cfg.Binaries.Tofu))
	}
	for _, t := range tools {
		if t.Status == toolcheck.StatusError {
			d.Logger.WarnContext(
				cmd.Context(),
				"cannot read the tool version",
				"tool",
				string(t.Tool),
				"error",
				t.Error,
			)
		}
	}
	return tools
}

// tooOldError fails doctor when a tool is below the minimum in version-policy.md.
func tooOldError(tools []toolcheck.Tool) error {
	var old []string
	for _, t := range tools {
		if t.Status == toolcheck.StatusTooOld {
			old = append(old, fmt.Sprintf("%s %s is older than the minimum %s", t.Tool, t.Version, t.Minimum))
		}
	}
	if len(old) == 0 {
		return nil
	}
	return errors.New(strings.Join(old, "; "))
}

func toolColumns() []string { return []string{"TOOL", "PATH", "VERSION", "MINIMUM", colStatus} }

func toolRow(t toolcheck.Tool) []string {
	status := map[toolcheck.Status]string{
		toolcheck.StatusOK:       "ok",
		toolcheck.StatusTooOld:   "too old",
		toolcheck.StatusNotFound: cellNotFound,
		toolcheck.StatusError:    cellError,
	}[t.Status]
	return []string{string(t.Tool), t.Path, dash(t.Version), t.Minimum, status}
}

// toolNotes names the gated features each found tool lacks.
func toolNotes(tools []toolcheck.Tool) []string {
	var out []string
	for _, t := range tools {
		var missing []string
		for _, f := range t.Features {
			switch {
			case f.Available:
			case f.Needs == "":
				missing = append(missing, f.Name+" (not in "+string(t.Tool)+")")
			default:
				missing = append(missing, f.Name+" (needs "+f.Needs+")")
			}
		}
		if len(missing) > 0 {
			out = append(out, string(t.Tool)+" "+t.Version+": unavailable: "+strings.Join(missing, ", "))
		}
	}
	return out
}

// checkAggregator probes the aggregator that scan and whois read the config layer from, and
// caches the result for them.
func checkAggregator(
	cmd *cobra.Command, l *accounts.Loader, prober *probe.Prober, agg config.Aggregator,
) *doctorAggregator {
	out := &doctorAggregator{Name: agg.Name, Profile: agg.Profile, Region: agg.Region}
	acct, err := aggregatorAccount(cmd.Context(), l, agg)
	if err != nil {
		out.Status, out.Error = probe.StatusError, err.Error()
		return out
	}
	rep := prober.Aggregator(cmd.Context(), acct, agg.Region, agg.Name)
	out.Region, out.Status, out.Error, out.Sources, out.Cached = rep.Region, rep.Status, rep.Error, rep.Sources, rep.Cached
	return out
}

func doctorSources(res *accounts.Result) []doctorSource {
	out := make([]doctorSource, 0, len(res.Accounts)+len(res.Errors))
	for _, a := range res.Accounts {
		out = append(out, doctorSource{
			Source: a.Source, Kind: a.Kind, Account: a.ID, Name: a.Name, RoleARN: a.RoleARN, Status: "ok",
		})
	}
	for _, e := range res.Errors {
		out = append(out, doctorSource{
			Source: e.Source, Kind: e.Kind, Account: e.Account, Name: e.Name, Status: "skipped", Reason: e.Err.Error(),
		})
	}
	slices.SortStableFunc(out, func(a, b doctorSource) int {
		return cmp.Or(strings.Compare(a.Source, b.Source), strings.Compare(a.Account, b.Account))
	})
	return out
}

func writeDoctor(d *app.Deps, format render.Format, data doctorData, res *accounts.Result) error {
	switch format {
	case render.FormatJSON:
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), data)
		for _, e := range res.Errors {
			env.Errors = append(env.Errors, render.Error{Target: sourceTarget(e), Message: e.Err.Error()})
		}
		return render.JSON(d.Stdout, env)
	case render.FormatMD:
		return writeDoctorMarkdown(d.Stdout, data)
	case render.FormatAuto,
		render.FormatTTY,
		render.FormatPlain,
		render.FormatCSV,
		render.FormatSARIF,
		render.FormatGitHub:
	}
	return writeDoctorText(d.Stdout, data)
}

func sourceTarget(e *accounts.SourceError) string {
	if e.Account != "" {
		return e.Source + "/" + e.Account
	}
	return e.Source
}

func sourceColumns() []string { return []string{"SOURCE", "KIND", colAccount, "NAME", colStatus} }

func sourceRow(s doctorSource) []string {
	return []string{s.Source, dash(string(s.Kind)), dash(s.Account), dash(s.Name), sourceStatus(s)}
}

// sourceStatus is short; the full reason is in the warning on stderr and in the JSON errors.
func sourceStatus(s doctorSource) string {
	switch {
	case s.Status == "ok":
		return "ok"
	case strings.HasPrefix(s.Reason, "SSO token"):
		return "skipped: SSO login needed"
	case strings.HasPrefix(s.Reason, "cannot assume"):
		return "skipped: cannot assume role"
	case strings.HasPrefix(s.Reason, "no usable credentials"):
		return "skipped: no credentials"
	default:
		return "skipped: error"
	}
}

func regionColumns() []string {
	return []string{colAccount, colRegion, "RESOURCE EXPLORER", "CONFIG", "AGGREGATORS", "LAYERS"}
}

func regionRow(r *probe.Report, reg *probe.Region) []string {
	layers := make([]string, len(reg.Layers))
	for i, l := range reg.Layers {
		layers[i] = string(l)
	}
	return []string{
		r.Account, reg.Region, reCell(r.ResourceExplorer.Status, reg.ResourceExplorer),
		configCell(reg.Config), aggCell(reg.Aggregators), strings.Join(layers, ", "),
	}
}

func reCell(account probe.Status, re probe.RegionRE) string {
	switch {
	case account == probe.StatusDenied || re.Status == probe.StatusDenied:
		return cellDenied
	case account == probe.StatusError || re.Status == probe.StatusError:
		return cellError
	case re.Index == "":
		return "no index"
	case re.DefaultView:
		return re.Index + ", default view"
	case re.Views > 0:
		return re.Index + ", " + strconv.Itoa(re.Views) + " views"
	default:
		return re.Index + ", no view"
	}
}

func configCell(c probe.ConfigCheck) string {
	switch {
	case c.Status == probe.StatusDenied:
		return cellDenied
	case c.Status == probe.StatusError:
		return cellError
	case len(c.Recorders) == 0:
		return "no recorder"
	}
	for _, r := range c.Recorders {
		if r.Recording {
			scope := "some types"
			if r.AllSupported {
				scope = "all supported"
			}
			return "recording (" + strings.ToLower(r.Frequency) + ", " + scope + ")"
		}
	}
	return "stopped"
}

func aggCell(a probe.AggCheck) string {
	switch a.Status {
	case probe.StatusDenied:
		return cellDenied
	case probe.StatusError:
		return cellError
	case probe.StatusAvailable:
		return strings.Join(a.Names, ", ")
	case probe.StatusUnavailable:
	}
	return "none"
}

func accountLabel(r *probe.Report) string {
	if r.Name != "" && r.Name != r.Account {
		return r.Account + " (" + r.Name + ")"
	}
	return r.Account
}

func aggregatorLine(a *doctorAggregator) string {
	where := strings.Join(
		slices.DeleteFunc([]string{a.Profile, a.Region}, func(s string) bool { return s == "" }),
		", ",
	)
	if where != "" {
		where = " (" + where + ")"
	}
	status := map[probe.Status]string{
		probe.StatusAvailable:   "found",
		probe.StatusUnavailable: cellNotFound,
		probe.StatusDenied:      cellDenied,
		probe.StatusError:       cellError,
	}[a.Status]
	if a.Error != "" {
		status += ": " + a.Error
	}
	if a.Status == probe.StatusAvailable {
		status += aggregatorSources(a.Sources)
	}
	return "AWS Config aggregator " + a.Name + where + ": " + status
}

// aggregatorSources summarizes the sources and says what scan does with them.
func aggregatorSources(sources []probe.AggSource) string {
	if len(sources) == 0 {
		return ", no sources. scan and whois query each account"
	}
	var regions []string
	failing := 0
	for _, s := range sources {
		if !slices.Contains(regions, s.Region) {
			regions = append(regions, s.Region)
		}
		if s.Status != probe.SyncSucceeded {
			failing++
		}
	}
	out := fmt.Sprintf(", %d %s in %d %s", len(sources), plural(len(sources), "source", "sources"),
		len(regions), plural(len(regions), "region", "regions"))
	if failing > 0 {
		out += fmt.Sprintf(", %d failed or outdated", failing)
	}
	return out + ". scan and whois read AWS Config through it where its sources are current"
}

func writeDoctorText(w io.Writer, data doctorData) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
	_, _ = fmt.Fprintln(tw, strings.Join(toolColumns(), "\t"))
	for _, t := range data.Tools {
		_, _ = fmt.Fprintln(tw, strings.Join(toolRow(t), "\t"))
	}
	if notes := toolNotes(data.Tools); len(notes) > 0 {
		_, _ = fmt.Fprintln(tw)
		for _, n := range notes {
			_, _ = fmt.Fprintln(tw, n)
		}
	}
	_, _ = fmt.Fprintln(tw)
	_, _ = fmt.Fprintln(tw, strings.Join(sourceColumns(), "\t"))
	for _, s := range data.Sources {
		_, _ = fmt.Fprintln(tw, strings.Join(sourceRow(s), "\t"))
	}
	if len(data.Accounts) > 0 {
		_, _ = fmt.Fprintln(tw)
		_, _ = fmt.Fprintln(tw, strings.Join(regionColumns(), "\t"))
		for _, r := range data.Accounts {
			for i := range r.Regions {
				_, _ = fmt.Fprintln(tw, strings.Join(regionRow(r, &r.Regions[i]), "\t"))
			}
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write doctor report: %w", err)
	}
	var b strings.Builder
	if data.Aggregator != nil {
		b.WriteString("\n" + aggregatorLine(data.Aggregator) + "\n")
	}
	for _, r := range data.Accounts {
		if len(r.Notes) == 0 {
			continue
		}
		b.WriteString("\n")
		for _, n := range r.Notes {
			b.WriteString(accountLabel(r) + ": " + n + "\n")
		}
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write doctor report: %w", err)
	}
	return nil
}

func writeDoctorMarkdown(w io.Writer, data doctorData) error {
	var b strings.Builder
	mdTable(&b, toolColumns(), len(data.Tools), func(i int) []string { return toolRow(data.Tools[i]) })
	if notes := toolNotes(data.Tools); len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
	}
	b.WriteString("\n")
	mdTable(&b, sourceColumns(), len(data.Sources), func(i int) []string { return sourceRow(data.Sources[i]) })
	type row struct {
		r   *probe.Report
		reg *probe.Region
	}
	var rows []row
	for _, r := range data.Accounts {
		for i := range r.Regions {
			rows = append(rows, row{r, &r.Regions[i]})
		}
	}
	if len(rows) > 0 {
		b.WriteString("\n")
		mdTable(&b, regionColumns(), len(rows), func(i int) []string { return regionRow(rows[i].r, rows[i].reg) })
	}
	if data.Aggregator != nil {
		b.WriteString("\n" + aggregatorLine(data.Aggregator) + "\n")
	}
	for _, r := range data.Accounts {
		if len(r.Notes) == 0 {
			continue
		}
		b.WriteString("\n## " + accountLabel(r) + "\n\n")
		for _, n := range r.Notes {
			b.WriteString("- " + n + "\n")
		}
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write doctor report: %w", err)
	}
	return nil
}

func mdTable(b *strings.Builder, cols []string, n int, row func(int) []string) {
	b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	b.WriteString(strings.Repeat("| --- ", len(cols)) + "|\n")
	for i := range n {
		cells := row(i)
		for j, c := range cells {
			cells[j] = strings.ReplaceAll(c, "|", `\|`)
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
}
