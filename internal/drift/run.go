package drift

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sassdavid/tfcensus/internal/execx"
)

// ReportFile is the name of the run report inside a run's output directory.
const ReportFile = "report.json"

// Runner runs external commands; *[execx.Runner] is one.
type Runner interface {
	Run(ctx context.Context, c execx.Cmd) (*execx.Result, error)
}

// RunOptions configures [RunPlans].
type RunOptions struct {
	Terragrunt string
	Terraform  string // TG_TF_PATH and --tf-path
	Filters    []string
	Mode       Mode
	Logger     *slog.Logger
}

// PlanArgs returns the terraform arguments after "--" for mode.
func PlanArgs(mode Mode) []string {
	if mode == ModeFull {
		return []string{"plan", "-lock=false", "-detailed-exitcode"}
	}
	return []string{"plan", "-refresh-only", "-lock=false", "-detailed-exitcode"}
}

// RunArgs returns the terragrunt arguments that plan every unit of a repo into outDir.
func RunArgs(opts RunOptions, outDir string) []string {
	args := []string{"run", "--all"}
	for _, f := range opts.Filters {
		args = append(args, "--filter", f)
	}
	args = append(args,
		"--tf-path", opts.Terraform,
		"--json-out-dir", filepath.Join(outDir, "plans"),
		"--report-file", filepath.Join(outDir, ReportFile),
		"--report-format", "json",
		"--log-format", "json",
		"--no-color",
		"--",
	)
	return append(args, PlanArgs(opts.Mode)...)
}

// RunPlans runs terragrunt run --all in repoDir and returns the plan directory and report it wrote
// under outDir. terragrunt exits 2 when a plan has changes and 1 when a unit failed; both still
// leave a report, so only a missing report is an error.
func RunPlans(ctx context.Context, r Runner, repoDir, outDir string, opts RunOptions) (Source, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(filepath.Join(outDir, "plans"), 0o700); err != nil {
		return Source{}, fmt.Errorf("create plan directory: %w", err)
	}
	progress := newProgress(ctx, opts.Logger)
	res, err := r.Run(ctx, execx.Cmd{
		Name:    opts.Terragrunt,
		Args:    RunArgs(opts, outDir),
		Dir:     repoDir,
		Env:     []string{"TG_TF_PATH=" + opts.Terraform},
		Stdout:  progress,
		Stderr:  progress,
		Timeout: -1,
	})
	progress.flush()
	src := Source{PlanDir: filepath.Join(outDir, "plans"), Report: filepath.Join(outDir, ReportFile)}
	if _, statErr := os.Stat(src.Report); statErr == nil {
		if err != nil {
			opts.Logger.InfoContext(ctx, "terragrunt run finished with a non-zero exit", "exit", exitCode(res))
		}
		return src, nil
	}
	if ee, ok := errors.AsType[*execx.ExitError](err); ok {
		return Source{}, fmt.Errorf("terragrunt run wrote no report: exit %d%s", ee.Code, stderrTail(res))
	}
	if err == nil {
		err = errors.New("exit 0")
	}
	return Source{}, fmt.Errorf("terragrunt run wrote no report: %w", err)
}

func exitCode(res *execx.Result) int {
	if res == nil {
		return -1
	}
	return res.ExitCode
}

// stderrTail returns the last line terragrunt wrote to stderr, as ": <line>", or "".
func stderrTail(res *execx.Result) string {
	if res == nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(res.Stderr)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	var entry struct {
		Msg string `json:"msg"`
	}
	if json.Unmarshal([]byte(last), &entry) == nil && entry.Msg != "" {
		last = entry.Msg
	}
	if last == "" {
		return ""
	}
	return ": " + last
}

// progress turns terragrunt's --log-format json lines into unit started and finished log records.
// A unit starts with its first plan line and finishes with terragrunt's show -json for the plan.
// Messages are never logged, because terraform output can hold attribute values.
type progress struct {
	ctx      context.Context //nolint:containedctx // the io.Writer interface has no context
	logger   *slog.Logger
	mu       sync.Mutex
	buf      bytes.Buffer
	started  map[string]bool
	finished map[string]bool
}

func newProgress(ctx context.Context, logger *slog.Logger) *progress {
	return &progress{ctx: ctx, logger: logger, started: map[string]bool{}, finished: map[string]bool{}}
}

func (p *progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf.Write(b)
	for {
		line, err := p.buf.ReadBytes('\n')
		if err != nil {
			p.buf.Write(line) // incomplete; keep it for the next write
			break
		}
		p.line(line)
	}
	return len(b), nil
}

func (p *progress) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf.Len() > 0 {
		p.line(p.buf.Bytes())
		p.buf.Reset()
	}
}

func (p *progress) line(b []byte) {
	var entry struct {
		Level   string   `json:"level"`
		WorkDir string   `json:"working-dir"`
		TFArgs  []string `json:"tf-command-args"`
	}
	if json.Unmarshal(bytes.TrimSpace(b), &entry) != nil || entry.WorkDir == "" {
		return
	}
	unit := strings.TrimPrefix(filepath.ToSlash(entry.WorkDir), "./")
	if !p.started[unit] {
		p.started[unit] = true
		p.logger.InfoContext(p.ctx, "unit started", "unit", unit)
	}
	if len(entry.TFArgs) > 0 && entry.TFArgs[0] == "show" && !p.finished[unit] {
		p.finished[unit] = true
		p.logger.InfoContext(p.ctx, "unit finished", "unit", unit)
	}
	if entry.Level == "error" {
		p.logger.WarnContext(p.ctx, "terragrunt reported an error", "unit", unit)
	}
}
