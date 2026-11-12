package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/sassdavid/tfcensus/internal/execx"
)

// OverrideFile is the file verify writes into a unit to point it at a local state copy. Terraform
// merges *_override.tf files over the unit's backend block.
const OverrideFile = "_tfcensus_override.tf"

const lockFileName = ".terraform.lock.hcl"

// ErrLeftover is returned when a unit still has an override file from an interrupted run.
var ErrLeftover = errors.New("override file left from an earlier run")

// Runner runs external commands; *[execx.Runner] is one.
type Runner interface {
	Run(ctx context.Context, c execx.Cmd) (*execx.Result, error)
}

// ResourceChange is one resource change of a plan. Only the address and actions are read.
type ResourceChange struct {
	Address   string   `json:"address"`
	Mode      string   `json:"mode,omitempty"`
	Actions   []string `json:"actions"`
	Importing bool     `json:"importing,omitempty"`
}

// Terragrunt runs init, plan, show and state push in a unit through terragrunt. Each run gets its
// own TF_DATA_DIR and download directory, so the unit's .terraform and .terragrunt-cache stay as
// they were.
type Terragrunt struct {
	Runner    Runner
	Bin       string
	Terraform string // TG_TF_PATH and --tf-path
	Logger    *slog.Logger
}

// session is one isolated set of directories for terragrunt calls in one unit.
type session struct {
	t   *Terragrunt
	dir string
	tmp string
}

func (t *Terragrunt) open(dir string) (*session, error) {
	tmp, err := os.MkdirTemp("", "tfcensus-tg-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	return &session{t: t, dir: dir, tmp: tmp}, nil
}

func (s *session) close() { _ = os.RemoveAll(s.tmp) }

func (s *session) run(ctx context.Context, tfArgs ...string) (*execx.Result, error) {
	args := append([]string{"run", "--tf-path", s.t.Terraform, "--"}, tfArgs...)
	res, err := s.t.Runner.Run(ctx, execx.Cmd{
		Name: s.t.Bin,
		Args: args,
		Dir:  s.dir,
		Env: []string{
			"TG_TF_PATH=" + s.t.Terraform,
			"TG_NON_INTERACTIVE=true",
			"TG_NO_COLOR=true",
			"TG_LOG_LEVEL=error",
			"TG_NO_AUTO_INIT=true",
			"TG_DOWNLOAD_DIR=" + filepath.Join(s.tmp, "cache"),
			"TF_DATA_DIR=" + filepath.Join(s.tmp, "data"),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
		},
		Timeout: -1,
	})
	if err != nil {
		return res, fmt.Errorf("terragrunt %s in %s: %w", tfArgs[0], s.dir, err)
	}
	return res, nil
}

// init runs terraform init, keeping the unit's lock file as it was.
func (s *session) init(ctx context.Context) error {
	restoreLock, err := keepFile(filepath.Join(s.dir, lockFileName))
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "init", "-input=false", "-no-color")
	return errors.Join(err, restoreLock())
}

// Plan plans the unit and returns its resource changes. With a state file, the unit reads that
// local copy through an override file instead of its backend; without one, it reads its backend.
// Plans never take the state lock. Errors carry no terraform output, which can hold attribute
// values.
func (t *Terragrunt) Plan(ctx context.Context, dir, stateFile string) ([]ResourceChange, error) {
	if stateFile != "" {
		remove, err := writeOverride(dir, stateFile)
		if err != nil {
			return nil, err
		}
		defer remove()
	}
	s, err := t.open(dir)
	if err != nil {
		return nil, err
	}
	defer s.close()
	if err = s.init(ctx); err != nil {
		return nil, quiet(err, "init", stateFile != "")
	}
	planFile := filepath.Join(s.tmp, "plan.tfplan")
	if _, err = s.run(ctx, "plan", "-lock=false", "-input=false", "-no-color", "-out="+planFile); err != nil {
		return nil, quiet(err, "plan", false)
	}
	res, err := s.run(ctx, "show", "-json", "-no-color", planFile)
	if err != nil {
		return nil, quiet(err, "show", false)
	}
	return ParsePlan(res.Stdout)
}

// Push runs terraform state push with file. Terraform takes the backend lock, refuses a different
// lineage or an older serial, and updates the DynamoDB digest.
func (t *Terragrunt) Push(ctx context.Context, dir, stateFile string) error {
	s, err := t.open(dir)
	if err != nil {
		return err
	}
	defer s.close()
	if err = s.init(ctx); err != nil {
		return err
	}
	_, err = s.run(ctx, "state", "push", stateFile)
	return err
}

// quiet drops terraform's stderr from err for commands whose output can hold attribute values.
func quiet(err error, step string, override bool) error {
	ee, ok := errors.AsType[*execx.ExitError](err)
	if !ok {
		return err
	}
	if step == "init" {
		hint := ""
		if override && strings.Contains(ee.Stderr, "-backend-config") {
			hint = "; the unit's remote_state must use generate, so the local override can replace the backend"
		}
		return fmt.Errorf("%w%s", err, hint)
	}
	return fmt.Errorf(
		"terragrunt %s exited with code %d (run terragrunt %s in the unit to see why)",
		step,
		ee.Code,
		step,
	)
}

func writeOverride(dir, stateFile string) (func(), error) {
	p := filepath.Join(dir, OverrideFile)
	if _, err := os.Lstat(p); err == nil {
		return nil, fmt.Errorf("%w: %s; delete it once no tfcensus move runs in this unit", ErrLeftover, p)
	}
	abs, err := filepath.Abs(stateFile)
	if err != nil {
		return nil, fmt.Errorf("resolve state file: %w", err)
	}
	// Terraform reads forward slashes on Windows too, and they need no escaping in HCL.
	path, _ := json.Marshal(filepath.ToSlash(abs)) //nolint:errchkjson // a string always marshals
	content := "# Written by tfcensus move for a plan against a local state copy. Delete it if it is still here.\n" +
		"terraform {\n  backend \"local\" {\n    path = " + string(path) + "\n  }\n}\n"
	if err = os.WriteFile(p, []byte(content), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", p, err)
	}
	return func() { _ = os.Remove(p) }, nil
}

// keepFile saves path and returns the function that puts it back as it was, or removes it when it
// didn't exist.
func keepFile(path string) (func() error, error) {
	orig, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return func() error {
		now, readErr := os.ReadFile(path)
		switch {
		case existed && (readErr != nil || !bytes.Equal(now, orig)):
			if werr := os.WriteFile(path, orig, 0o644); werr != nil { //nolint:gosec // G306: a lock file is committed.
				return fmt.Errorf("restore %s: %w", path, werr)
			}
		case !existed && readErr == nil:
			if rerr := os.Remove(path); rerr != nil {
				return fmt.Errorf("remove %s: %w", path, rerr)
			}
		}
		return nil
	}, nil
}

// ParsePlan reads the resource changes of terraform show -json output.
func ParsePlan(b []byte) ([]ResourceChange, error) {
	var p struct {
		FormatVersion   string `json:"format_version"`
		ResourceChanges []struct {
			Address string `json:"address"`
			Mode    string `json:"mode"`
			Change  struct {
				Actions   []string        `json:"actions"`
				Importing json.RawMessage `json:"importing"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse plan json: %w", err)
	}
	if p.FormatVersion == "" {
		return nil, errors.New("parse plan json: no format_version")
	}
	out := make([]ResourceChange, 0, len(p.ResourceChanges))
	for _, rc := range p.ResourceChanges {
		out = append(out, ResourceChange{
			Address: rc.Address, Mode: rc.Mode, Actions: rc.Change.Actions,
			Importing: len(rc.Change.Importing) > 0 && string(rc.Change.Importing) != jsonNull,
		})
	}
	return out, nil
}
