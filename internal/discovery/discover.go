package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/execx"
)

// RenderMode says when to run terragrunt render.
type RenderMode string

// Render modes, set by discovery.render.
const (
	RenderAuto   RenderMode = "auto"   // the fast path first, render for units it can't resolve
	RenderAlways RenderMode = "always" // render every unit
	RenderNever  RenderMode = "never"  // the fast path only
)

// ParseRenderMode validates a discovery.render value.
func ParseRenderMode(s string) (RenderMode, error) {
	switch m := RenderMode(s); m {
	case RenderAuto, RenderAlways, RenderNever:
		return m, nil
	default:
		return "", fmt.Errorf("invalid render mode %q: want auto, always or never", s)
	}
}

const defaultTerraform = "terraform"

// DefaultRenderTimeout bounds one terragrunt render, which can run run_cmd or sops.
const DefaultRenderTimeout = 5 * time.Minute

// Runner runs external commands; *[execx.Runner] is one.
type Runner interface {
	Run(ctx context.Context, c execx.Cmd) (*execx.Result, error)
}

// Options configures a [Discoverer].
type Options struct {
	Terragrunt     string // terragrunt binary
	Terraform      string // passed to terragrunt as --tf-path
	Filters        []string
	GenerateStacks bool
	Render         RenderMode
	Concurrency    int // parallel terragrunt render calls
	RenderTimeout  time.Duration
	LookupEnv      func(string) (string, bool) // for get_env in the fast path
	WorkDir        string                      // relative repo paths are resolved against it
}

// Discoverer finds units in repositories.
type Discoverer struct {
	runner Runner
	logger *slog.Logger
	opts   Options
}

// New returns a Discoverer. Zero Options fields get defaults.
func New(runner Runner, logger *slog.Logger, opts Options) *Discoverer {
	if opts.Terragrunt == "" {
		opts.Terragrunt = "terragrunt"
	}
	if opts.Terraform == "" {
		opts.Terraform = defaultTerraform
	}
	if opts.Render == "" {
		opts.Render = RenderAuto
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = runtime.NumCPU()
	}
	if opts.RenderTimeout == 0 {
		opts.RenderTimeout = DefaultRenderTimeout
	}
	return &Discoverer{runner: runner, logger: logger, opts: opts}
}

// RepoError is a repository that couldn't be discovered at all.
type RepoError struct {
	Repo string
	Err  error
}

func (e *RepoError) Error() string { return e.Repo + ": " + e.Err.Error() }

func (e *RepoError) Unwrap() error { return e.Err }

// Result is the outcome of [Discoverer.Discover].
type Result struct {
	Units  []Unit
	Errors []*RepoError
}

// Discover lists the units in each repository. A repository that fails is reported in
// Result.Errors and doesn't stop the others.
func (d *Discoverer) Discover(ctx context.Context, repos []string) Result {
	var res Result
	for _, repo := range repos {
		if ctx.Err() != nil {
			res.Errors = append(res.Errors, &RepoError{Repo: repo, Err: context.Cause(ctx)})
			continue
		}
		units, err := d.repo(ctx, repo)
		if err != nil {
			d.logger.ErrorContext(ctx, "discovery failed", "repo", repo, "error", err)
			res.Errors = append(res.Errors, &RepoError{Repo: displayPath(repo), Err: err})
		}
		res.Units = append(res.Units, units...)
	}
	sortUnits(res.Units)
	return res
}

func displayPath(p string) string { return filepath.ToSlash(filepath.Clean(p)) }

func (d *Discoverer) repo(ctx context.Context, repo string) ([]Unit, error) {
	abs := repo
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(d.opts.WorkDir, abs)
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	// Terragrunt relativizes paths against the real directory, so a symlinked repo path would
	// produce dependency paths that climb out of the repo.
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	if fi, statErr := os.Stat(abs); statErr != nil || !fi.IsDir() {
		return nil, errors.New("not a directory")
	}
	tg, err := isTerragruntRepo(abs)
	if err != nil {
		return nil, err
	}
	if !tg {
		if len(d.opts.Filters) > 0 {
			d.logger.WarnContext(ctx, "--filter applies to terragrunt repos only", "repo", repo)
		}
		units, plainErr := plainRoots(displayPath(repo), abs)
		d.logger.InfoContext(ctx, "discovered plain terraform roots", "repo", repo, "units", len(units))
		return units, plainErr
	}
	units, err := d.terragruntRepo(ctx, displayPath(repo), abs)
	d.logger.InfoContext(ctx, "discovered terragrunt units", "repo", repo, "units", len(units))
	return units, err
}

// isTerragruntRepo reports whether dir holds any terragrunt configuration.
func isTerragruntRepo(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if skipDir(e.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		switch e.Name() {
		case unitFileName, rootFileName, stackFileName:
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("scan %s: %w", dir, err)
	}
	return found, nil
}

func skipDir(name string) bool {
	switch name {
	case ".git", ".terraform", ".terragrunt-cache", "node_modules":
		return true
	}
	return false
}

func (d *Discoverer) terragruntRepo(ctx context.Context, repo, abs string) ([]Unit, error) {
	if d.opts.GenerateStacks {
		if err := d.generateStacks(ctx, abs); err != nil {
			return nil, err
		}
	}
	items, err := d.find(ctx, abs)
	if err != nil {
		return nil, err
	}
	units := make([]Unit, 0, len(items))
	for _, it := range items {
		u := Unit{Repo: repo, Path: filepath.ToSlash(it.Path), Warnings: []string{}}
		switch it.Type {
		case "stack":
			u.Kind = KindTerragruntStack
			if !stackGenerated(u.Path, items) {
				u.warn("not generated: run with --generate-stacks to list its units")
			}
		case "unit":
			u.Kind = KindTerragrunt
			for _, dep := range it.Dependencies {
				u.Dependencies = append(u.Dependencies, filepath.ToSlash(dep))
			}
			if excluded(it.Exclude) {
				u.Excluded = true
				u.warn(excludeWarning(it.Exclude))
			}
		default:
			d.logger.DebugContext(ctx, "skipping unknown find entry", "repo", repo, "type", it.Type, "unit", it.Path)
			continue
		}
		units = append(units, u)
	}
	d.resolve(ctx, abs, units)
	return units, nil
}

// resolve fills in the backend and target of every terragrunt unit, by the fast path, render, or
// both, as the render mode says.
func (d *Discoverer) resolve(ctx context.Context, abs string, units []Unit) {
	ev := newEvaluator(d.opts.LookupEnv)
	rel := relativizer(abs)
	fastErrs := make([]error, len(units))
	var toRender []int
	for i := range units {
		u := &units[i]
		if u.Kind != KindTerragrunt {
			continue
		}
		if d.opts.Render == RenderAlways {
			toRender = append(toRender, i)
			continue
		}
		cfg, err := ev.evalUnit(filepath.Join(abs, filepath.FromSlash(u.Path), unitFileName))
		if err == nil {
			err = applyConfig(u, cfg, MethodFastPath, rel)
		}
		switch {
		case err == nil:
		case d.opts.Render == RenderAuto:
			fastErrs[i] = err
			toRender = append(toRender, i)
		default:
			u.unresolved(rel("fast path: " + err.Error()))
		}
	}

	g := new(errgroup.Group)
	g.SetLimit(d.opts.Concurrency)
	for _, i := range toRender {
		g.Go(func() error {
			d.renderUnit(ctx, abs, &units[i], fastErrs[i], rel)
			return nil
		})
	}
	_ = g.Wait() // renderUnit records failures on the unit and never returns an error
}

// renderUnit resolves u with terragrunt render. fastErr is why the fast path failed, if it ran.
func (d *Discoverer) renderUnit(ctx context.Context, abs string, u *Unit, fastErr error, rel func(string) string) {
	out, err := d.render(ctx, abs, u.Path)
	var cfg *unitConfig
	if err == nil {
		cfg, err = out.unitConfig()
	}
	if err == nil {
		err = applyConfig(u, cfg, MethodRender, rel)
	}
	switch {
	case err != nil && fastErr != nil:
		u.unresolved(rel(fmt.Sprintf("fast path: %v; render: %v", fastErr, err)))
	case err != nil:
		u.unresolved(rel("render: " + err.Error()))
	case fastErr != nil:
		u.warn(rel("resolved by render; the fast path failed: " + fastErr.Error()))
	}
}

func applyConfig(u *Unit, cfg *unitConfig, m Method, rel func(string) string) error {
	b, warnings, err := backendFrom(cfg.backend, cfg.config, cfg.configErr)
	if err != nil {
		return err
	}
	u.setBackend(b, m)
	for _, w := range warnings {
		u.warn(rel(w))
	}
	u.Target = resolveTarget(providerFromGenerated(cfg.generated), cfg.inputs, b)
	return nil
}

// relativizer strips the repo's absolute path from messages and uses forward slashes, so they read
// the same on every machine.
func relativizer(abs string) func(string) string {
	prefix := abs + string(filepath.Separator)
	return func(s string) string {
		s = strings.ReplaceAll(s, prefix, "")
		if filepath.Separator != '/' {
			s = strings.ReplaceAll(s, string(filepath.Separator), "/")
		}
		return s
	}
}
