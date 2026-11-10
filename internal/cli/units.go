package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/render"
)

const (
	flagFilter         = "filter"
	flagGenerateStacks = "generate-stacks"
	flagRender         = "render"
)

func newUnitsCmd(d *app.Deps, g *globals) *cobra.Command {
	var filters []string
	cmd := &cobra.Command{
		Use:   "units [paths...]",
		Short: "List units with their state backend, target account and region",
		Long: `units lists the Terragrunt units and plain Terraform/OpenTofu root modules under each path,
with where each one keeps its state and which AWS account and region it manages.

A path with a terragrunt.hcl, root.hcl or terragrunt.stack.hcl anywhere below it is a Terragrunt
repo: terragrunt find lists its units. Any other path is searched for root modules, which are
directories whose .tf files have a backend or cloud block.

tfcensus evaluates remote_state itself (the fast path). get_aws_account_id there returns the
account of the default AWS credentials (AWS_PROFILE, environment credentials or an instance role),
looked up once with sts:GetCallerIdentity and only when state needs it. With --render auto, units
the fast path can't evaluate, for example because they call run_cmd or set iam_role, go to
terragrunt render --json. Units that neither method resolves are listed with the reason. Without paths, units uses discovery.roots from
the config, or the working directory.`,
		Example: `  tfcensus units ~/src/infra/acme-live
  tfcensus units --filter './live/prod/**' --format json
  tfcensus units --render never --generate-stacks`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.outFormat.Check(
				render.FormatTTY,
				render.FormatPlain,
				render.FormatMD,
				render.FormatJSON,
			); err != nil {
				return usageError(err)
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			cfg := res.Config
			mode, err := discovery.ParseRenderMode(cfg.Discovery.Render)
			if err != nil {
				return usageError(err)
			}
			cwd, err := d.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			runner := execx.NewRunner(d.Logger, execx.WithEnviron(func() []string { return d.Env }))
			loader := newLoader(d)
			disc := discovery.New(runner, d.Logger, discovery.Options{
				Terragrunt:     cfg.Binaries.Terragrunt,
				Terraform:      cfg.Binaries.Terraform,
				Filters:        filters,
				GenerateStacks: cfg.Discovery.GenerateStacks,
				Render:         mode,
				LookupEnv:      d.Env.Lookup,
				WorkDir:        cwd,
				AccountID: func(ctx context.Context) (string, error) {
					acct, accountErr := loader.Default(ctx)
					if accountErr != nil {
						return "", accountErr
					}
					return acct.ID, nil
				},
			})
			result := disc.Discover(cmd.Context(), unitPaths(args, cfg.Discovery.Roots, d.Env.Get("HOME")))
			if err = writeUnits(d.Stdout, g.outFormat, d, result); err != nil {
				return err
			}
			if len(result.Errors) > 0 {
				errs := make([]error, len(result.Errors))
				for i, e := range result.Errors {
					errs[i] = e
				}
				return fmt.Errorf("discover units: %w", errors.Join(errs...))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&filters, flagFilter, nil,
		"terragrunt filter expression, passed to terragrunt find unchanged (repeatable)")
	f.Bool(flagGenerateStacks, false,
		"run terragrunt stack generate first, so units in stacks are listed (config discovery.generate_stacks)")
	f.String(flagRender, "auto",
		"when to use terragrunt render: auto, always or never (config discovery.render)")
	return cmd
}

// unitPaths returns the paths to discover: the arguments, else the configured roots, else ".".
func unitPaths(args, roots []string, home string) []string {
	if len(args) > 0 {
		return args
	}
	if len(roots) == 0 {
		return []string{"."}
	}
	out := make([]string, len(roots))
	for i, r := range roots {
		if rest, ok := strings.CutPrefix(r, "~/"); ok && home != "" {
			r = filepath.Join(home, rest)
		}
		out[i] = r
	}
	return out
}

type unitsData struct {
	Units []discovery.Unit `json:"units"`
}

func writeUnits(w io.Writer, format render.Format, d *app.Deps, res discovery.Result) error {
	units := res.Units
	if units == nil {
		units = []discovery.Unit{}
	}
	switch format {
	case render.FormatJSON:
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), unitsData{Units: units})
		for _, e := range res.Errors {
			env.Errors = append(env.Errors, render.Error{Target: e.Repo, Message: e.Err.Error()})
		}
		return render.JSON(w, env)
	case render.FormatMD:
		return writeUnitsMarkdown(w, units)
	case render.FormatAuto, render.FormatTTY, render.FormatPlain, render.FormatSARIF:
		return writeUnitsTable(w, units)
	}
	return writeUnitsTable(w, units)
}

func unitColumns() []string {
	return []string{"REPO", "UNIT", "KIND", "BACKEND", "LOCK", colAccount, "REGION", "RESOLUTION", "WARNINGS"}
}

func unitRow(u *discovery.Unit) []string {
	return []string{
		u.Repo, u.Path, string(u.Kind), backendCell(u.Backend), dash(string(u.Lock)),
		withConfidence(u.Target.Account, u.Target.Confidence), dash(u.Target.Region),
		dash(string(u.Resolution)), strconv.Itoa(len(u.Warnings)),
	}
}

func backendCell(b *discovery.Backend) string {
	switch {
	case b == nil:
		return "-"
	case b.Type != discovery.BackendS3:
		return b.Type
	case b.Region == "":
		return "s3://" + b.Bucket + "/" + b.Key
	default:
		return "s3://" + b.Bucket + "/" + b.Key + " (" + b.Region + ")"
	}
}

func withConfidence(account string, c discovery.Confidence) string {
	if account == "" {
		return "-"
	}
	if c == discovery.ConfidenceLow || c == discovery.ConfidenceMedium {
		return account + " (" + string(c) + ")"
	}
	return account
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// writeUnitsTable prints aligned columns, then the reasons and warnings that don't fit in a cell.
func writeUnitsTable(w io.Writer, units []discovery.Unit) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
	_, _ = fmt.Fprintln(tw, strings.Join(unitColumns(), "\t"))
	for i := range units {
		_, _ = fmt.Fprintln(tw, strings.Join(unitRow(&units[i]), "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write units: %w", err)
	}
	var notes []string
	for _, u := range units {
		if u.Reason != "" {
			notes = append(notes, fmt.Sprintf("%s/%s: unresolved: %s", u.Repo, u.Path, u.Reason))
		}
		for _, warning := range u.Warnings {
			notes = append(notes, fmt.Sprintf("%s/%s: %s", u.Repo, u.Path, warning))
		}
	}
	if len(notes) > 0 {
		_, _ = fmt.Fprintln(w)
		for _, n := range notes {
			_, _ = fmt.Fprintln(w, n)
		}
	}
	_, _ = fmt.Fprintln(w)
	_, err := fmt.Fprintln(w, unitsSummary(units))
	if err != nil {
		return fmt.Errorf("write units: %w", err)
	}
	return nil
}

func unitsSummary(units []discovery.Unit) string {
	counts := make(map[discovery.Method]int)
	n := 0
	for _, u := range units {
		if u.Kind == discovery.KindTerragruntStack {
			continue
		}
		n++
		counts[u.Resolution]++
	}
	return fmt.Sprintf("%d units: %d fast-path, %d render, %d static, %d unresolved", n,
		counts[discovery.MethodFastPath], counts[discovery.MethodRender], counts[discovery.MethodStatic],
		counts[discovery.MethodUnresolved])
}

func writeUnitsMarkdown(w io.Writer, units []discovery.Unit) error {
	cols := unitColumns()
	cols[len(cols)-1] = "NOTES"
	var b strings.Builder
	b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	b.WriteString(strings.Repeat("| --- ", len(cols)) + "|\n")
	for i := range units {
		u := &units[i]
		row := unitRow(u)
		notes := u.Warnings
		if u.Reason != "" {
			notes = append([]string{"unresolved: " + u.Reason}, notes...)
		}
		row[len(row)-1] = strings.Join(notes, "; ")
		for j, cell := range row {
			row[j] = strings.ReplaceAll(cell, "|", `\|`)
		}
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write units: %w", err)
	}
	return nil
}
