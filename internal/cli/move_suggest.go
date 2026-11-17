package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/migrate"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/suggest"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const (
	flagTo      = "to"
	flagPlanDir = "plan-dir"
	flagIgnore  = "ignore"
	flagDraft   = "draft"
)

const (
	suggestSourceTerragrunt = "terragrunt"
	suggestSourcePlanDir    = "plan-dir"
)

type suggestArgs struct {
	from, to []string
	ignore   []string
	planDir  string
	draft    string
	repo     string
	force    bool
}

// suggestReport is the data of move suggest --format json (schemas/move-suggest-v1.json).
type suggestReport struct {
	From        []string            `json:"from"`
	To          []string            `json:"to"`
	Source      string              `json:"source"`
	Ignore      []string            `json:"ignore"`
	Comparisons int                 `json:"comparisons"`
	Moves       []suggest.Move      `json:"moves"`
	Unmatched   []suggest.Unmatched `json:"unmatched"`
	DraftFile   string              `json:"draft_file,omitempty"`
	Draft       string              `json:"draft"`
}

func newMoveSuggestCmd(d *app.Deps, g *globals) *cobra.Command {
	a := suggestArgs{}
	cmd := &cobra.Command{
		Use:   "suggest --from <unit> --to <unit>",
		Short: "Draft a migration file from the moves that plans show",
		Long: `suggest plans each --from and --to unit through terragrunt (one at a time, against its backend,
without the state lock) and pairs every resource a --from unit plans to delete with the resource a
--to unit plans to create. A pair is a move when the type is the same, every attribute known before
apply is equal, and neither resource matches anything else. Null equals an empty string, list or
map. Replaced resources and data sources never take part. Name a unit in both --from and --to to
find renames inside it.

--ignore lets one attribute differ, with tfautomv's syntax: everything:TYPE:ATTR,
whitespace:TYPE:ATTR, prefix:TYPE:ATTR:PREFIX, plus json:TYPE:ATTR, which compares JSON strings by
value. TYPE may be *. ATTR is a flat key such as tags.Name or ingress.0.cidr_blocks.#; a * segment
matches any one segment, and a rule covers the keys below its ATTR.

The draft goes to stdout, or to --draft. It holds one migration "units" block per source unit and
one block of mv actions per unit whose addresses change. suggest writes no state; check the draft
with tfcensus move plan. --plan-dir reads <dir>/<unit>/tfplan.json instead of running terragrunt,
the layout of terragrunt run --all --json-out-dir.`,
		Example: "  tfcensus move suggest --from live/network --to live/network-sg --to live/network-endpoints\n" +
			"  tfcensus move suggest --from live/app --to live/app --ignore json:aws_iam_policy:policy " +
			"--draft migrations/app.hcl",
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			return runMoveSuggest(cmd, d, &res.Config, a, g.outFormat)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&a.from, flagFrom, nil, "unit whose planned deletes can be moved, as path or path:workspace "+
		"(repeatable)")
	f.StringArrayVar(&a.to, flagTo, nil, "unit whose planned creates can receive a move, as path or path:workspace "+
		"(repeatable)")
	f.StringArrayVar(&a.ignore, flagIgnore, nil, "let an attribute differ: KIND:TYPE:ATTR[:PREFIX] (repeatable)")
	f.StringVar(&a.planDir, flagPlanDir, "", "read plans from <dir>/<unit>/tfplan.json instead of running terragrunt")
	f.StringVar(&a.draft, flagDraft, "", "write the draft migration file here instead of stdout")
	f.BoolVar(&a.force, flagForce, false, "overwrite an existing --draft file")
	f.StringVar(&a.repo, flagRepo, ".", "repository root that unit paths are relative to")
	_ = cmd.MarkFlagRequired(flagFrom)
	_ = cmd.MarkFlagRequired(flagTo)
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagFrom:    complete.NoFiles,
		flagTo:      complete.NoFiles,
		flagIgnore:  complete.NoFiles,
		flagPlanDir: complete.Dirs(),
		flagDraft:   complete.Files("hcl"),
		flagRepo:    complete.Dirs(),
	})
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
	return cmd
}

func runMoveSuggest(cmd *cobra.Command, d *app.Deps, cfg *config.Config, a suggestArgs, format render.Format) error {
	rules := make([]suggest.Rule, 0, len(a.ignore))
	for _, s := range a.ignore {
		r, err := suggest.ParseRule(s)
		if err != nil {
			return usageError(fmt.Errorf("--%s: %w", flagIgnore, err))
		}
		rules = append(rules, r)
	}
	units, err := suggestUnits(a)
	if err != nil {
		return usageError(err)
	}
	draftPath := ""
	if a.draft != "" {
		if draftPath, err = absPath(d, a.draft); err != nil {
			return err
		}
		if _, statErr := os.Stat(draftPath); statErr == nil && !a.force {
			return usageError(fmt.Errorf("%s exists; pass --%s to overwrite it", a.draft, flagForce))
		}
	}

	rep := suggestReport{
		From: a.from, To: a.to, Source: suggestSourceTerragrunt, Ignore: a.ignore, DraftFile: a.draft,
	}
	if rep.Ignore == nil {
		rep.Ignore = []string{}
	}
	if a.planDir != "" {
		rep.Source = suggestSourcePlanDir
		err = readSuggestPlans(d, a.planDir, units)
	} else {
		err = runSuggestPlans(cmd, d, cfg, a.repo, units)
	}
	if err != nil {
		return err
	}
	res, err := suggest.Match(units, rules)
	if err != nil {
		return err
	}
	rep.Comparisons, rep.Moves, rep.Unmatched = res.Comparisons, res.Moves, res.Unmatched
	draft := suggest.Draft(res.Moves)
	rep.Draft = string(draft)
	if draft == nil {
		rep.DraftFile = ""
	}
	if draftPath != "" && draft != nil {
		if err = os.WriteFile(draftPath, draft, 0o600); err != nil {
			return fmt.Errorf("write draft: %w", err)
		}
	}
	return writeSuggest(d, format, &rep)
}

// suggestUnits checks the unit references and returns one unit per reference, with its sides.
func suggestUnits(a suggestArgs) ([]suggest.Unit, error) {
	var units []suggest.Unit
	index := map[string]int{}
	add := func(refs []string, flag string, from bool) error {
		for _, ref := range refs {
			c, err := migrate.CleanRef(ref)
			if err != nil {
				return fmt.Errorf("--%s: %w", flag, err)
			}
			if _, ws := migrate.SplitRef(c); a.planDir != "" && ws != tfstate.DefaultWorkspace {
				return fmt.Errorf("--%s %s: --%s holds one plan per unit path, so it can't name a workspace",
					flag, ref, flagPlanDir)
			}
			i, ok := index[c]
			if !ok {
				i = len(units)
				index[c] = i
				units = append(units, suggest.Unit{Ref: c})
			}
			if from {
				units[i].From = true
			} else {
				units[i].To = true
			}
		}
		return nil
	}
	if err := add(a.from, flagFrom, true); err != nil {
		return nil, err
	}
	if err := add(a.to, flagTo, false); err != nil {
		return nil, err
	}
	return units, nil
}

func absPath(d *app.Deps, p string) (string, error) {
	if filepath.IsAbs(p) {
		return p, nil
	}
	cwd, err := d.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Join(cwd, p), nil
}

func readSuggestPlans(d *app.Deps, dir string, units []suggest.Unit) error {
	root, err := absPath(d, dir)
	if err != nil {
		return err
	}
	for i := range units {
		file := filepath.Join(root, filepath.FromSlash(units[i].Ref), drift.PlanFile)
		b, readErr := os.ReadFile(file)
		if errors.Is(readErr, fs.ErrNotExist) {
			return usageError(fmt.Errorf("--%s: no plan for %s at %s", flagPlanDir, units[i].Ref, file))
		}
		if readErr != nil {
			return fmt.Errorf("read plan of %s: %w", units[i].Ref, readErr)
		}
		if units[i].Plan, err = parseSuggestPlan(units[i].Ref, b); err != nil {
			return err
		}
	}
	return nil
}

// runSuggestPlans plans each unit through terragrunt, one at a time.
func runSuggestPlans(cmd *cobra.Command, d *app.Deps, cfg *config.Config, repo string, units []suggest.Unit) error {
	ctx := cmd.Context()
	dirs, err := suggestDirs(cmd, d, cfg, repo, units)
	if err != nil {
		return err
	}
	tg := &migrate.Terragrunt{
		Runner:    execx.NewRunner(d.Logger, execx.WithEnviron(func() []string { return d.Env })),
		Bin:       cfg.Binaries.Terragrunt,
		Terraform: cfg.Binaries.Terraform,
		Logger:    d.Logger,
	}
	for i := range units {
		_, ws := migrate.SplitRef(units[i].Ref)
		d.Logger.InfoContext(ctx, "planning unit", "unit", units[i].Ref)
		b, planErr := tg.PlanJSON(ctx, dirs[i], ws)
		if planErr != nil {
			return fmt.Errorf("plan %s: %w", units[i].Ref, planErr)
		}
		if units[i].Plan, err = parseSuggestPlan(units[i].Ref, b); err != nil {
			return err
		}
	}
	return nil
}

// suggestDirs discovers the repository and returns each unit's directory.
func suggestDirs(cmd *cobra.Command, d *app.Deps, cfg *config.Config, repo string, units []suggest.Unit) (
	[]string, error,
) {
	cwd, err := d.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	disc, err := discoverUnits(cmd, d, cfg, nil, []string{repo}, newLoader(d))
	if err != nil {
		return nil, err
	}
	byPath := map[string]*discovery.Unit{}
	for i := range disc.Units {
		byPath[disc.Units[i].Path] = &disc.Units[i]
	}
	dirs := make([]string, len(units))
	var errs []error
	for i := range units {
		p, _ := migrate.SplitRef(units[i].Ref)
		u, ok := byPath[p]
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s is not a unit under %s", migrate.ErrUnknown, p, repo))
			continue
		}
		root := filepath.FromSlash(u.Repo)
		if !filepath.IsAbs(root) {
			root = filepath.Join(cwd, root)
		}
		dirs[i] = filepath.Join(root, filepath.FromSlash(u.Path))
	}
	if len(errs) > 0 {
		return nil, usageError(errors.Join(append(errs, discoveryError(disc))...))
	}
	return dirs, nil
}

// parseSuggestPlan reads a plan. Its errors name the unit only, never plan content.
func parseSuggestPlan(unit string, b []byte) (*tfjson.Plan, error) {
	var p tfjson.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse plan of %s: not plan JSON from terraform show -json", unit)
	}
	if p.FormatVersion == "" {
		return nil, fmt.Errorf("parse plan of %s: no format_version", unit)
	}
	return &p, nil
}

func writeSuggest(d *app.Deps, format render.Format, rep *suggestReport) error {
	if format == render.FormatJSON {
		return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), rep))
	}
	if rep.DraftFile == "" && rep.Draft != "" {
		if _, err := fmt.Fprint(d.Stdout, rep.Draft); err != nil {
			return fmt.Errorf("write draft: %w", err)
		}
	}
	return writeSuggestSummary(d, rep)
}

// writeSuggestSummary writes what suggest found to stderr, so stdout holds only the draft.
func writeSuggestSummary(d *app.Deps, rep *suggestReport) error {
	w := &errWriter{w: d.Stderr}
	w.printf("move suggest: %d comparison(s), %d move(s)\n", rep.Comparisons, len(rep.Moves))
	for _, m := range rep.Moves {
		w.printf("  %s %s -> %s %s\n", m.FromUnit, m.FromAddress, m.ToUnit, m.ToAddress)
	}
	if len(rep.Unmatched) > 0 {
		w.printf("not moved:\n")
	}
	for _, u := range rep.Unmatched {
		w.printf("  %s %s %s: %s\n", u.Action, u.Unit, u.Address, unmatchedReason(&u))
	}
	switch {
	case rep.Draft == "":
		w.printf("no moves found; no draft written\n")
	case rep.DraftFile != "":
		w.printf("wrote %s; check it with tfcensus move plan %s\n", rep.DraftFile, rep.DraftFile)
	}
	return w.err
}

func unmatchedReason(u *suggest.Unmatched) string {
	switch {
	case len(u.Candidates) > 1:
		refs := make([]string, 0, len(u.Candidates))
		for _, c := range u.Candidates {
			refs = append(refs, c.Unit+" "+c.Address)
		}
		return fmt.Sprintf("%d matches (%s)", len(u.Candidates), strings.Join(refs, ", "))
	case len(u.Candidates) == 1:
		c := u.Candidates[0]
		return "its match " + c.Unit + " " + c.Address + " also matches another resource"
	case u.Closest != nil:
		return fmt.Sprintf("closest is %s %s, which differs in %s", u.Closest.Unit, u.Closest.Address,
			joinList(u.Closest.Differ))
	}
	return "no resource of the same type"
}

func joinList(ss []string) string {
	const shown = 5
	if len(ss) <= shown {
		return strings.Join(ss, ", ")
	}
	return strings.Join(ss[:shown], ", ") + fmt.Sprintf(" and %d more", len(ss)-shown)
}

// errWriter keeps the first write error, so a summary of many lines needs one check.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}
