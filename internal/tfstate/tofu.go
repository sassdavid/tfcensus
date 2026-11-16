package tfstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/execx"
)

// ErrDecrypt is returned when tofu can't read an encrypted state. Its messages never carry tofu
// output, which can hold attribute values.
var ErrDecrypt = errors.New("decrypt state with tofu")

// Runner runs external commands; *[execx.Runner] is one.
type Runner interface {
	Run(ctx context.Context, c execx.Cmd) (*execx.Result, error)
}

// Tofu reads encrypted state by running tofu init and tofu show -json in the unit, through
// terragrunt for a terragrunt unit. Each call gets its own TF_DATA_DIR and terragrunt download
// directory, and the unit's lock file is put back, so the unit stays as it was. Neither command
// takes the state lock or writes state.
type Tofu struct {
	Runner     Runner
	Bin        string // the tofu binary
	Terragrunt string // the terragrunt binary, for terragrunt units
}

// Decrypt returns the tofu show -json output for one workspace of the unit in dir. The key
// configuration is the unit's encryption block or TF_ENCRYPTION in the environment.
func (t *Tofu) Decrypt(ctx context.Context, u *discovery.Unit, dir, workspace string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "tfcensus-tofu-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	restore, err := KeepLockFile(dir)
	if err != nil {
		return nil, err
	}
	out, err := t.show(ctx, u, dir, workspace, tmp)
	return out, errors.Join(err, restore())
}

func (t *Tofu) show(ctx context.Context, u *discovery.Unit, dir, workspace, tmp string) ([]byte, error) {
	if _, err := t.run(ctx, u, dir, workspace, tmp, "init", "-input=false", "-no-color", "-lock=false"); err != nil {
		return nil, err
	}
	res, err := t.run(ctx, u, dir, workspace, tmp, "show", "-json", "-no-color")
	if err != nil {
		return nil, err
	}
	return res.Stdout, nil
}

func (t *Tofu) run(ctx context.Context, u *discovery.Unit, dir, workspace, tmp string, args ...string) (
	*execx.Result, error,
) {
	env := []string{
		"TF_DATA_DIR=" + filepath.Join(tmp, "data"),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
	}
	if workspace != "" && workspace != DefaultWorkspace {
		env = append(env, "TF_WORKSPACE="+workspace)
	}
	c := execx.Cmd{Name: t.Bin, Args: args, Dir: dir, Env: env}
	missing := t.Bin + " not found; install OpenTofu or set --tofu-path (config binaries.tofu)"
	if u.Kind == discovery.KindTerragrunt {
		missing = t.Terragrunt + " not found; install it or set --tg-path (config binaries.terragrunt)"
		c.Name = t.Terragrunt
		c.Args = append([]string{"run", "--tf-path", t.Bin, "--"}, args...)
		c.Env = slices.Concat(env, []string{
			"TG_TF_PATH=" + t.Bin,
			"TG_NON_INTERACTIVE=true",
			"TG_NO_COLOR=true",
			"TG_LOG_LEVEL=error",
			"TG_NO_AUTO_INIT=true",
			"TG_DOWNLOAD_DIR=" + filepath.Join(tmp, "cache"),
		})
	}
	res, err := t.Runner.Run(ctx, c)
	if err != nil {
		return nil, decryptError(err, args[0], missing)
	}
	return res, nil
}

// decryptError turns a failed tofu call into a fixed message. It drops [execx.ExitError], whose
// text holds tofu's stderr.
func decryptError(err error, step, missing string) error {
	if step == "show" {
		step = "show -json"
	}
	if ee, ok := errors.AsType[*execx.ExitError](err); ok {
		hint := "check the unit's backend configuration and its credentials"
		if step != "init" {
			hint = "check the key configuration in the unit's encryption block or TF_ENCRYPTION"
		}
		return fmt.Errorf("%w: tofu %s exited with code %d; %s (run it in the unit to see why)",
			ErrDecrypt, step, ee.Code, hint)
	}
	switch {
	case errors.Is(err, execx.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrDecrypt, missing)
	case errors.Is(err, execx.ErrTimeout):
		return fmt.Errorf("%w: tofu %s timed out", ErrDecrypt, step)
	}
	return fmt.Errorf("%w: tofu %s: %w", ErrDecrypt, step, err)
}
