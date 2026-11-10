package cli

import (
	"cmp"
	"fmt"
	"io"
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
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/render"
)

const (
	probeConcurrency = 4
	colAccount       = "ACCOUNT"
	cellDenied       = "denied"
	cellError        = "error"
)

func newDoctorCmd(d *app.Deps, g *globals) *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check AWS credentials and which inventory layers each account and region supports",
		Long: `doctor loads the credentials of every selected account source and probes each account and
region for the inventory layers a scan can use:

  re      Resource Explorer: an index in the region and a view to search it with
  config  AWS Config: a recorder that is recording
  cc      Cloud Control: always available, but slow and throttled
  native  tfcensus's own enumerators: always available

Without --profile or --org, doctor uses the default AWS credentials (AWS_PROFILE, environment
credentials or an instance role). Without --region, it probes the regions that have a Resource
Explorer index and the profile's own region.

doctor is read-only: it never creates indexes, views or recorders. Its notes say what an admin
could turn on. Results are cached for 24 hours under $XDG_CACHE_HOME/tfcensus/probe; --refresh
probes again.

A source whose credentials fail, such as a profile with an expired SSO token, is skipped with a
warning. doctor exits 4 when every source fails.`,
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
			return runDoctor(cmd, d, g.outFormat, &res.Config, refresh)
		},
	}
	addAccountFlags(cmd)
	cmd.Flags().BoolVar(&refresh, "refresh", false, "probe again even when the cached result is fresh")
	return cmd
}

type doctorData struct {
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
	Name    string       `json:"name"`
	Profile string       `json:"profile,omitempty"`
	Region  string       `json:"region,omitempty"`
	Status  probe.Status `json:"status"`
	Error   string       `json:"error,omitempty"`
}

func runDoctor(cmd *cobra.Command, d *app.Deps, format render.Format, cfg *config.Config, refresh bool) error {
	ctx := cmd.Context()
	loader := newLoader(d)
	res, err := resolveAccounts(cmd, d, loader, cfg)
	if err != nil {
		return err
	}
	prober, err := newProber(d, refresh)
	if err != nil {
		return err
	}
	data := doctorData{Sources: doctorSources(res), Accounts: make([]*probe.Report, len(res.Accounts))}
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(probeConcurrency)
	for i, acct := range res.Accounts {
		eg.Go(func() error {
			var probeErr error
			data.Accounts[i], probeErr = prober.Probe(egctx, acct, cfg.Regions)
			return probeErr
		})
	}
	if err = eg.Wait(); err != nil {
		return fmt.Errorf("probe accounts: %w", err)
	}
	if agg := cfg.Accounts.Aggregator; agg.Name != "" {
		data.Aggregator = checkAggregator(cmd, loader, agg)
	}
	if err = writeDoctor(d, format, data, res); err != nil {
		return err
	}
	if len(res.Accounts) == 0 && len(res.Errors) > 0 {
		return noAccountsError(res)
	}
	return nil
}

func checkAggregator(cmd *cobra.Command, l *accounts.Loader, agg config.Aggregator) *doctorAggregator {
	out := &doctorAggregator{Name: agg.Name, Profile: agg.Profile, Region: agg.Region}
	var acct *accounts.Account
	var err error
	if agg.Profile != "" {
		acct, err = l.Profile(cmd.Context(), agg.Profile)
	} else {
		acct, err = l.Default(cmd.Context())
	}
	if err != nil {
		out.Status, out.Error = probe.StatusError, err.Error()
		return out
	}
	c := probe.Aggregator(cmd.Context(), acct.Config, agg.Region, agg.Name)
	out.Status, out.Error = c.Status, c.Error
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
	case render.FormatAuto, render.FormatTTY, render.FormatPlain, render.FormatSARIF:
	}
	return writeDoctorText(d.Stdout, data)
}

func sourceTarget(e *accounts.SourceError) string {
	if e.Account != "" {
		return e.Source + "/" + e.Account
	}
	return e.Source
}

func sourceColumns() []string { return []string{"SOURCE", "KIND", colAccount, "NAME", "STATUS"} }

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
	return []string{colAccount, "REGION", "RESOURCE EXPLORER", "CONFIG", "AGGREGATORS", "LAYERS"}
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
		probe.StatusUnavailable: "not found",
		probe.StatusDenied:      cellDenied,
		probe.StatusError:       cellError,
	}[a.Status]
	if a.Error != "" {
		status += ": " + a.Error
	}
	return "AWS Config aggregator " + a.Name + where + ": " + status
}

func writeDoctorText(w io.Writer, data doctorData) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
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
