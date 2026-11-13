package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/render"
)

const (
	flagMode           = "mode"
	flagCollapse       = "collapse"
	flagFromJSONOutDir = "from-json-out-dir"
	flagReportFile     = "report-file"
	flagShowAttributes = "show-attributes"
)

// Gates --fail-on accepts for drift.
const (
	gateDrift   = "drift"
	gateReverts = "drift:reverts"
	gateErrors  = "errors"
)

type driftArgs struct {
	filters, paths []string
	fromDir        string
	reportFile     string
	failOn         []string
	minCount       int
	showAttributes bool
}

func newDriftCmd(d *app.Deps, g *globals) *cobra.Command {
	a := driftArgs{}
	formats := []render.Format{
		render.FormatTTY,
		render.FormatPlain,
		render.FormatMD,
		render.FormatJSON,
		render.FormatSARIF,
		render.FormatGitHub,
	}
	cmd := &cobra.Command{
		Use:   "drift [paths...]",
		Short: "Find drift between the units' state and AWS",
		Long: `drift runs a plan in every Terragrunt unit and reports the resources that changed outside
terraform, attribute by attribute. It runs

  terragrunt run --all [--filter ...] --tf-path <bin> --json-out-dir <tmp> --report-file <tmp>/report.json
    --report-format json --log-format json -- plan -refresh-only -lock=false -detailed-exitcode

in each repo, with TG_TF_PATH set, and reads every unit's tfplan.json and the run report. Plain
Terraform root modules are skipped. With --mode full, the plan isn't refresh-only, and drift tells
the drift the plan reverts (the code disagrees) from the drift it keeps.

--from-json-out-dir reads plans an earlier terragrunt run wrote, without running anything. Pass the
same run's --report-file, so units that failed before writing a plan are reported.

Identical changes in several units become one group (--collapse shape compares type, action and
attribute paths; strict also compares values; none keeps one group per change). Noise rules under
drift.noise hide attribute changes or whole changes; the presets aws-tags-all and computed-hashes
hide tags_all updates and computed hashes. A rule past its expires date stops hiding drift and
drift warns. drift.never_hide (default delete and replace) lists actions no rule can hide.

drift prints attribute paths, never values. --show-attributes adds before and after values;
sensitive values show as (sensitive) and unknown ones as (known after apply).

The exit code is 3 when a --fail-on gate counts at least --min-count: drift counts drifted
resources, drift:reverts the ones the plan reverts (--mode full only), errors the failed units. It
is 1 when a unit or a repo failed and no gate matched, and 0 otherwise. terraform's
-detailed-exitcode (2 for changes) is read internally and never returned.`,
		Example: `  tfcensus drift ~/src/infra/acme-live --filter './live/prod/**' --fail-on drift
  tfcensus drift --mode full --fail-on drift:reverts --format md > drift.md
  tfcensus drift --from-json-out-dir out --report-file out/report.json --format github \
    --out sarif=drift.sarif --out md=drift.md --fail-on drift`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.checkFormats(formats...); err != nil {
				return usageError(err)
			}
			if a.minCount < 1 {
				return usageError(fmt.Errorf("--%s must be at least 1, got %d", flagMinCount, a.minCount))
			}
			if a.fromDir != "" && len(args) > 0 {
				return usageError(
					fmt.Errorf("--%s takes no paths, got %q", flagFromJSONOutDir, strings.Join(args, " ")),
				)
			}
			if a.reportFile != "" && a.fromDir == "" {
				return usageError(fmt.Errorf("--%s needs --%s", flagReportFile, flagFromJSONOutDir))
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			a.paths = args
			return runDrift(cmd, d, g.sink(cmd, d), &res.Config, a)
		},
	}
	addDiscoveryFlags(cmd, &a.filters)
	f := cmd.Flags()
	f.String(flagMode, string(drift.ModeRefreshOnly),
		complete.Usage("plan mode: {values}; full also tells reverted drift from kept drift (config drift.mode)",
			driftModeChoices()))
	f.String(flagCollapse, string(drift.CollapseShape),
		complete.Usage("group identical changes across units: {values} (config drift.collapse)", collapseChoices()))
	f.StringVar(&a.fromDir, flagFromJSONOutDir, "",
		"read the plans of an earlier terragrunt run --json-out-dir instead of running terragrunt")
	f.StringVar(&a.reportFile, flagReportFile, "", "the JSON --report-file of that run")
	f.StringSliceVar(&a.failOn, flagFailOn, nil,
		complete.Usage("exit 3 when these gates match: {values}", driftGateChoices()))
	f.IntVar(&a.minCount, flagMinCount, 1, "count a --fail-on gate needs before it exits 3")
	f.BoolVar(&a.showAttributes, flagShowAttributes, false,
		"show before and after values; sensitive and unknown values stay masked")
	addOutputFlags(cmd, g, formats...)
	cmd.MarkFlagsMutuallyExclusive(flagFromJSONOutDir, flagFilter)
	cmd.MarkFlagsMutuallyExclusive(flagFromJSONOutDir, flagGenerateStacks)
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagMode:           complete.Enum(driftModeChoices()),
		flagCollapse:       complete.Enum(collapseChoices()),
		flagFromJSONOutDir: complete.Dirs(),
		flagReportFile:     complete.Files("json"),
		flagFailOn:         complete.List(driftGateChoices()),
		flagMinCount:       complete.NoFiles,
	})
	return cmd
}

func driftModeChoices() []complete.Choice {
	return []complete.Choice{
		{Value: string(drift.ModeRefreshOnly), Desc: "plan -refresh-only: drift only"},
		{Value: string(drift.ModeFull), Desc: "plan: drift, and whether the plan reverts it"},
	}
}

func collapseChoices() []complete.Choice {
	return []complete.Choice{
		{Value: string(drift.CollapseShape), Desc: "same type, action and attribute paths"},
		{Value: string(drift.CollapseStrict), Desc: "shape, and the same before and after values"},
		{Value: string(drift.CollapseNone), Desc: "one group per change"},
	}
}

func driftGateChoices() []complete.Choice {
	return []complete.Choice{
		{Value: gateDrift, Desc: "any unit drifted"},
		{Value: gateReverts, Desc: "a full plan would revert drift"},
		{Value: gateErrors, Desc: "a unit failed"},
	}
}

// driftSettings are the validated config and flags.
type driftSettings struct {
	mode     drift.Mode
	collapse drift.Collapse
	noise    *drift.NoiseRules
	gates    []string
}

func newDriftSettings(cfg *config.Config, a driftArgs) (*driftSettings, error) {
	s := &driftSettings{}
	var err error
	if s.mode, err = drift.ParseMode(cfg.Drift.Mode); err != nil {
		return nil, usageError(err)
	}
	if s.collapse, err = drift.ParseCollapse(cfg.Drift.Collapse); err != nil {
		return nil, usageError(err)
	}
	if s.noise, err = drift.NewNoiseRules(cfg.Drift.Noise, cfg.Drift.NeverHide); err != nil {
		return nil, usageError(err)
	}
	for _, gate := range a.failOn {
		gate = strings.TrimSpace(gate)
		switch gate {
		case gateDrift, gateErrors:
		case gateReverts:
			if s.mode != drift.ModeFull {
				return nil, usageError(fmt.Errorf("--%s %s needs --%s full", flagFailOn, gateReverts, flagMode))
			}
		default:
			return nil, usageError(fmt.Errorf("--%s %q: want drift, drift:reverts or errors", flagFailOn, gate))
		}
		s.gates = append(s.gates, gate)
	}
	return s, nil
}

func runDrift(cmd *cobra.Command, d *app.Deps, sink render.Sink, cfg *config.Config, a driftArgs) error {
	s, err := newDriftSettings(cfg, a)
	if err != nil {
		return err
	}
	var (
		inputs   []drift.Input
		warnings []string
		problems []driftProblem
	)
	if a.fromDir != "" {
		inputs, warnings, err = drift.Load(drift.Source{PlanDir: a.fromDir, Report: a.reportFile})
		if err != nil {
			return usageError(err)
		}
	} else {
		inputs, warnings, problems, err = runDriftPlans(cmd, d, cfg, a, s.mode)
		if err != nil {
			return err
		}
	}
	rep := drift.Analyze(inputs, warnings, drift.Options{
		Mode: s.mode, Collapse: s.collapse, Noise: s.noise, ShowAttributes: a.showAttributes, Now: d.Clock(),
	})
	for _, w := range rep.Warnings {
		d.Logger.WarnContext(cmd.Context(), w)
	}
	if err = sink.Write(func(w io.Writer, f render.Format) error {
		return writeDrift(w, d, f, rep, problems)
	}); err != nil {
		return err
	}
	return driftGate(rep, problems, s.gates, a.minCount)
}

// driftProblem is a repo that couldn't be discovered or planned.
type driftProblem struct {
	Target, Message string
}

// runDriftPlans discovers the units under the paths and runs terragrunt once per repo. A repo that
// fails becomes a problem; the others still run.
func runDriftPlans(cmd *cobra.Command, d *app.Deps, cfg *config.Config, a driftArgs, mode drift.Mode) (
	[]drift.Input, []string, []driftProblem, error,
) {
	ctx := cmd.Context()
	disc, err := discoverUnits(cmd, d, cfg, a.filters, a.paths, newLoader(d))
	if err != nil {
		return nil, nil, nil, err
	}
	var problems []driftProblem
	for _, e := range disc.Errors {
		problems = append(problems, driftProblem{Target: e.Repo, Message: e.Err.Error()})
	}
	repos, targets, warnings := driftRepos(disc)
	tmp, err := os.MkdirTemp("", "tfcensus-drift-")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create plan directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	cwd, err := d.Getwd()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get working directory: %w", err)
	}
	runner := execx.NewRunner(d.Logger, execx.WithEnviron(func() []string { return d.Env }))
	opts := drift.RunOptions{
		Terragrunt: cfg.Binaries.Terragrunt, Terraform: cfg.Binaries.Terraform, Filters: a.filters, Mode: mode,
		Logger: d.Logger,
	}
	var inputs []drift.Input
	for i, repo := range repos {
		dir, dirErr := repoDir(cwd, repo)
		var src drift.Source
		if dirErr == nil {
			src, dirErr = drift.RunPlans(ctx, runner, dir, filepath.Join(tmp, strconv.Itoa(i)), opts)
		}
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		if dirErr != nil {
			problems = append(problems, driftProblem{Target: repo, Message: dirErr.Error()})
			continue
		}
		src.Prefix = repo
		more, moreWarnings, loadErr := drift.Load(src)
		if loadErr != nil {
			problems = append(problems, driftProblem{Target: repo, Message: loadErr.Error()})
			continue
		}
		inputs = append(inputs, more...)
		warnings = append(warnings, moreWarnings...)
	}
	for i := range inputs {
		if t, ok := targets[inputs[i].Unit.Path]; ok {
			inputs[i].Unit.Account, inputs[i].Unit.Region = t.Account, t.Region
		}
	}
	return inputs, warnings, problems, nil
}

// driftRepos returns the repos with terragrunt units, the target of each unit by its repo-prefixed
// path, and a warning per repo of plain root modules.
func driftRepos(disc discovery.Result) ([]string, map[string]discovery.Target, []string) {
	targets := map[string]discovery.Target{}
	plain := map[string]int{}
	var repos []string
	for i := range disc.Units {
		u := &disc.Units[i]
		switch u.Kind {
		case discovery.KindTerragrunt:
			if !slices.Contains(repos, u.Repo) {
				repos = append(repos, u.Repo)
			}
			targets[u.Repo+"/"+u.Path] = u.Target
		case discovery.KindTerraform:
			plain[u.Repo]++
		case discovery.KindTerragruntStack:
		}
	}
	slices.Sort(repos)
	var warnings []string
	for repo, n := range plain {
		warnings = append(
			warnings,
			fmt.Sprintf("%s: skipped %d plain terraform root module(s); drift runs terragrunt units", repo, n),
		)
	}
	slices.Sort(warnings)
	return repos, targets, warnings
}

// repoDir resolves a repo path as discovery did, symlinks included, because terragrunt names units
// relative to the real directory.
func repoDir(cwd, repo string) (string, error) {
	dir := repo
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, filepath.FromSlash(dir))
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}
	return resolved, nil
}

// driftGate returns exit 3 when a gate reaches minCount, else an error when a unit or repo failed.
func driftGate(rep *drift.Report, problems []driftProblem, gates []string, minCount int) error {
	failed := len(rep.FailedUnits()) + len(problems)
	counts := map[string]int{gateDrift: rep.Summary.Resources, gateReverts: rep.Summary.Reverts, gateErrors: failed}
	var matched []string
	for _, gate := range gates {
		if counts[gate] >= minCount {
			matched = append(matched, fmt.Sprintf("%s: %d", gate, counts[gate]))
		}
	}
	if len(matched) > 0 {
		return &exitError{
			code: ExitFindings,
			err:  fmt.Errorf("--%s matched (%s)", flagFailOn, strings.Join(matched, ", ")),
		}
	}
	if failed > 0 {
		return errors.New(cmp.Or(
			failedText(len(rep.FailedUnits()), len(problems)), "drift incomplete",
		) + " (see the report above)")
	}
	return nil
}

func failedText(units, repos int) string {
	var parts []string
	if units > 0 {
		parts = append(parts, fmt.Sprintf("%d unit(s) failed", units))
	}
	if repos > 0 {
		parts = append(parts, fmt.Sprintf("%d repo(s) failed", repos))
	}
	return strings.Join(parts, " and ")
}
