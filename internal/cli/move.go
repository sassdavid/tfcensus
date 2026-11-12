package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/migrate"
	"github.com/sassdavid/tfcensus/internal/prompt"
	"github.com/sassdavid/tfcensus/internal/render"
)

const (
	flagBlock = "block"
	flagVar   = "var"
	flagYes   = "yes"
)

type moveCommand string

const (
	movePlan        moveCommand = "plan"
	moveApply       moveCommand = "apply"
	moveRollback    moveCommand = "rollback"
	moveMarkApplied moveCommand = "mark-applied"
	moveStatus      moveCommand = "status"
)

// writes reports whether the command writes to a backend.
func (c moveCommand) writes() bool {
	return c == moveApply || c == moveRollback || c == moveMarkApplied
}

type moveArgs struct {
	blocks []string
	vars   []string
	repo   string
	yes    bool
	force  bool
}

func newMoveCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move",
		Short: "Move resources between the states of Terragrunt units",
		Long: `move runs migration files: migration "units" blocks that move resources from one unit's state
to others, or run tfmigrate-style mv, xmv and rm actions inside one state. See
docs/runbooks/move.md for the file format and a full walkthrough.

plan and status only read. apply, rollback and mark-applied are the only tfcensus commands that
write to a backend; each asks you to type the block's destination unit path first, or takes
--yes. Without a terminal and without --yes they exit 2 and change nothing.

Unit paths in the file are relative to --repo (default: the current directory). Only the default
workspace of units with an s3 backend can be moved.`,
	}
	cmd.AddCommand(
		newMoveSubCmd(d, g, movePlan, "Verify a migration file with plans, without writing anything",
			`plan reads each involved unit's state from S3, applies the block in memory and plans every
unit against its new state through a local backend override (_tfcensus_override.tf), with its
own credentials. A surgery block passes when every resource change is no-op or read; output
changes don't count. Blocks run in order, so a later block sees an earlier one's result.

A native block only lists the files apply would write.

The exit code is 3 when a block is rejected (MV-VERIFY, MV-PLAN, MV-ACCOUNT or MV-VERSIONING).`),
		newMoveSubCmd(d, g, moveApply, "Apply a migration file to the units' states",
			`apply plans and verifies each pending block like plan, then asks you to type the block's last
destination unit path (its from path for an in-place block). It checks that no state changed since
it was read, pushes the destinations and then the source with terragrunt run -- state push (so
terraform takes the lock and checks lineage and serial), and records the S3 version IDs in each
unit's <key>.tfcensus-migrations.json. Blocks already applied are skipped.

A native block writes moves_tfcensus_<block>.tf into each unit (import blocks in destinations,
removed blocks in the source), then plans each unit to check the files. It changes no state and
no history; run mark-applied after your normal apply.

apply needs bucket versioning (S3-VER). The exit code is 3 when a block is rejected.`),
		newMoveSubCmd(d, g, moveRollback, "Restore the states a migration file changed",
			`rollback restores every unit of each applied block to the S3 object version it had before
apply pushed it, byte for byte, newest block first and the source before its destinations. It takes
the backend lock, copies the old version over the current one, fixes the DynamoDB digest, and marks
the history records as rolled back. A unit that didn't exist before is deleted again.

rollback stops when a state changed after the push, unless --force is set.`),
		newMoveSubCmd(d, g, moveMarkApplied, "Record migration blocks as applied without changing state",
			`mark-applied writes a history record for each block in every unit it touches and changes no
state. Use it after a native block's apply, or for a migration done by hand. Such records can't be
rolled back by tfcensus.`),
		newMoveSubCmd(d, g, moveStatus, "Show whether each migration block is applied in each unit",
			`status reads the history object next to each involved unit's state and shows each block as
applied, pending or partial (applied in some units only).`),
	)
	return cmd
}

func newMoveSubCmd(d *app.Deps, g *globals, which moveCommand, short, long string) *cobra.Command {
	a := moveArgs{}
	cmd := &cobra.Command{
		Use:   string(which) + " <migration-file>",
		Short: short,
		Long:  long,
		Args:  exactArgs(1),
		Example: fmt.Sprintf("  tfcensus move %s migrations/split_network.hcl\n"+
			"  tfcensus move %s migrations/split_network.hcl --block split_network --var env=prod", which, which),
		RunE: func(cmd *cobra.Command, args []string) error {
			allowed := []render.Format{render.FormatTTY, render.FormatPlain, render.FormatJSON}
			if which == movePlan || which == moveApply {
				allowed = append(allowed, render.FormatSARIF)
			}
			if err := g.outFormat.Check(allowed...); err != nil {
				return usageError(err)
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			return runMove(cmd, d, g, &res.Config, which, args[0], a)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&a.blocks, flagBlock, nil, "run only these blocks (default: all, in file order)")
	f.StringArrayVar(&a.vars, flagVar, nil, "set var.NAME in the migration file, as NAME=VALUE")
	f.StringVar(&a.repo, flagRepo, ".", "repository root that the file's unit paths are relative to")
	if which.writes() {
		f.BoolVar(&a.yes, flagYes, false, "don't ask for confirmation")
	}
	switch which {
	case moveApply:
		f.BoolVar(&a.force, flagForce, false, "overwrite moves_tfcensus_<block>.tf files of native blocks")
	case moveRollback:
		f.BoolVar(&a.force, flagForce, false, "restore even when a state changed after the push")
	case movePlan, moveMarkApplied, moveStatus:
	}
	return cmd
}

// exactArgs is cobra.ExactArgs with exit code 2.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return usageError(err)
		}
		return nil
	}
}

func parseVars(vars []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vars {
		k, val, ok := strings.Cut(v, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--%s %q: want NAME=VALUE", flagVar, v)
		}
		out[k] = val
	}
	return out, nil
}

func envMap(env app.Env) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

func runMove(
	cmd *cobra.Command, d *app.Deps, g *globals, cfg *config.Config, which moveCommand, file string, a moveArgs,
) error {
	ctx := cmd.Context()
	vars, err := parseVars(a.vars)
	if err != nil {
		return usageError(err)
	}
	if !filepath.IsAbs(file) {
		cwd, cwdErr := d.Getwd()
		if cwdErr != nil {
			return fmt.Errorf("get working directory: %w", cwdErr)
		}
		file = filepath.Join(cwd, file)
	}
	mf, err := migrate.ParseFile(file, envMap(d.Env), vars)
	if err != nil {
		return usageError(err)
	}
	blocks, err := migrate.Select(mf, a.blocks)
	if err != nil {
		return usageError(err)
	}
	loader := newLoader(d)
	units, refs, err := moveUnits(cmd, d, cfg, loader, a.repo, blocks)
	if err != nil {
		return err
	}
	var accts *accounts.Result
	if len(cfg.Accounts.Profiles) > 0 || cfg.Accounts.Org.Enabled {
		if accts, err = resolveAccounts(cmd, d, loader, cfg); err != nil {
			return err
		}
	}
	runner := execx.NewRunner(d.Logger, execx.WithEnviron(func() []string { return d.Env }))
	tg := &migrate.Terragrunt{
		Runner: runner, Bin: cfg.Binaries.Terragrunt, Terraform: cfg.Binaries.Terraform, Logger: d.Logger,
	}
	o := migrate.Options{
		Units:   units,
		Clients: moveClients(newBackends(loader, accts, cfg, d), refs, which.writes()),
		Planner: tg,
		Logger:  d.Logger,
		Clock:   d.Clock,
	}
	if which == moveApply {
		o.Pusher = tg
	}
	eng, err := migrate.New(o)
	if err != nil {
		return err
	}
	defer func() { _ = eng.Close() }()

	gate := prompt.Gate{Yes: a.yes}
	if prompt.Allowed(readerIsTerminal(d.Stdin), app.IsTerminal(d.Stdout), d.Env.Lookup, g.noInput) {
		gate.Prompter = prompt.NewLine(d.Stdin, d.Stderr)
	}
	rep, err := runMoveCommand(ctx, eng, which, mf, blocks, gate, a.force)
	if rep != nil && len(rep.Blocks) > 0 {
		if werr := writeMove(d, g.outFormat, rep); werr != nil {
			return errors.Join(err, werr)
		}
	}
	if err == nil && rejected(rep) {
		err = migrate.ErrRejected
	}
	return moveError(err)
}

func runMoveCommand(
	ctx context.Context, eng *migrate.Engine, which moveCommand, mf *migrate.File, blocks []*migrate.Block,
	gate prompt.Gate, force bool,
) (*migrate.Report, error) {
	switch which {
	case moveApply:
		return eng.Apply(ctx, mf, blocks, gate, force)
	case moveRollback:
		return eng.Rollback(ctx, mf, blocks, gate, force)
	case moveMarkApplied:
		return eng.MarkApplied(ctx, mf, blocks, gate)
	case moveStatus:
		return eng.Status(ctx, mf, blocks)
	case movePlan:
	}
	return eng.Plan(ctx, mf, blocks)
}

func rejected(rep *migrate.Report) bool {
	if rep == nil {
		return false
	}
	for i := range rep.Blocks {
		if len(rep.Blocks[i].Findings) > 0 {
			return true
		}
	}
	return false
}

func moveError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, prompt.ErrNoInput), errors.Is(err, migrate.ErrFile), errors.Is(err, migrate.ErrUnknown):
		return usageError(err)
	case errors.Is(err, migrate.ErrRejected):
		return &exitError{code: ExitFindings, err: err}
	case errors.Is(err, accounts.ErrAuth):
		return &exitError{code: ExitAuth, err: err}
	}
	return err
}

func readerIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && app.IsTerminal(f)
}

// moveUnits discovers the repository and returns the units the blocks name, with the
// credentials of their backends.
func moveUnits(
	cmd *cobra.Command, d *app.Deps, cfg *config.Config, loader *accounts.Loader, repo string, blocks []*migrate.Block,
) (map[string]*migrate.Unit, map[string]accounts.BackendRef, error) {
	cwd, err := d.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("get working directory: %w", err)
	}
	disc, err := discoverUnits(cmd, d, cfg, nil, []string{repo}, loader)
	if err != nil {
		return nil, nil, err
	}
	refs := map[string]accounts.BackendRef{}
	byPath := map[string]*discovery.Unit{}
	for i := range disc.Units {
		byPath[disc.Units[i].Path] = &disc.Units[i]
	}
	out := map[string]*migrate.Unit{}
	var errs []error
	for _, b := range blocks {
		for _, p := range b.Units() {
			if _, done := out[p]; done {
				continue
			}
			u, ok := byPath[p]
			switch {
			case !ok:
				errs = append(errs, fmt.Errorf("%w: %s is not a unit under %s", migrate.ErrUnknown, p, repo))
				continue
			case u.Backend == nil || u.Backend.Type != discovery.BackendS3 || u.Backend.Bucket == "" || u.Backend.Key == "":
				errs = append(errs, fmt.Errorf("%w: %s has no resolved s3 backend%s", migrate.ErrUnknown, p,
					reasonSuffix(u.Reason)))
				continue
			}
			root := filepath.FromSlash(u.Repo)
			if !filepath.IsAbs(root) {
				root = filepath.Join(cwd, root)
			}
			be := u.Backend
			out[p] = &migrate.Unit{
				Path: p, Dir: filepath.Join(root, filepath.FromSlash(u.Path)),
				Bucket: be.Bucket, Key: be.Key, Region: cmp.Or(be.Region, u.Target.Region),
				Table: be.DynamoDBTable, Lock: u.Lock, Account: u.Target.Account,
			}
			refs[p] = backendRef(u)
		}
	}
	if len(errs) > 0 {
		return nil, nil, errors.Join(append(errs, discoveryError(disc))...)
	}
	return out, refs, nil
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// moveClients returns the backend clients of a unit, from its backend credentials. Read-only
// commands get no writer.
func moveClients(b *accounts.Backends, refs map[string]accounts.BackendRef, write bool) migrate.ClientsFunc {
	return func(ctx context.Context, u *migrate.Unit) (*migrate.Clients, error) {
		acct, region, err := b.Credentials(ctx, refs[u.Path])
		if err != nil {
			return nil, err
		}
		s3c := b.S3(acct, region)
		c := &migrate.Clients{Reader: s3c, By: cmp.Or(acct.RoleARN, "profile "+acct.Source)}
		if write {
			c.Writer = s3c
			if u.Table != "" {
				c.Table = dynamodb.NewFromConfig(acct.Config, func(o *dynamodb.Options) { o.Region = region })
			}
		}
		return c, nil
	}
}
