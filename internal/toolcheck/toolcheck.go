package toolcheck

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/go-version"

	"github.com/sassdavid/tfcensus/internal/execx"
)

// Kind names a tool.
type Kind string

// The tools doctor checks.
const (
	KindTerragrunt Kind = "terragrunt"
	KindTerraform  Kind = "terraform"
	KindTofu       Kind = "tofu"
)

// Status is the outcome of checking one tool.
type Status string

// The statuses of a checked tool.
const (
	StatusOK       Status = "ok"
	StatusTooOld   Status = "too-old"   // below the minimum
	StatusNotFound Status = "not-found" // not on PATH or not executable
	StatusError    Status = "error"     // it ran but its version couldn't be read
)

// Tool is the result of checking one binary.
type Tool struct {
	Tool     Kind      `json:"tool"`
	Path     string    `json:"path"`
	Version  string    `json:"version,omitempty"`
	Minimum  string    `json:"minimum"`
	Status   Status    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Features []Feature `json:"features,omitempty"`
}

// Feature is a feature that needs a newer version than the minimum.
type Feature struct {
	Name      string `json:"name"`
	Needs     string `json:"needs,omitempty"` // empty: this tool never has it
	Available bool   `json:"available"`
}

// Runner runs a command, as [execx.Runner] does.
type Runner interface {
	Run(ctx context.Context, c execx.Cmd) (*execx.Result, error)
}

const timeout = 30 * time.Second

func minimum(k Kind) string {
	return map[Kind]string{KindTerragrunt: "1.0.0", KindTerraform: "1.5.0", KindTofu: "1.6.0"}[k]
}

// gated lists each tool's gated features with the version that has them.
func gated(k Kind) []Feature {
	switch k {
	case KindTerraform:
		return []Feature{
			{Name: "removed and import for_each", Needs: "1.7.0"},
			{Name: "use_lockfile", Needs: "1.10.0"},
			{Name: "resource identity", Needs: "1.12.0"},
			{Name: "query", Needs: "1.14.0"},
		}
	case KindTofu:
		return []Feature{
			{Name: "removed and import for_each", Needs: "1.7.0"},
			{Name: "removed lifecycle and use_lockfile", Needs: "1.10.0"},
			{Name: "resource identity", Needs: "1.12.0"},
			{Name: "query"},
		}
	case KindTerragrunt:
	}
	return nil
}

// Check runs tgPath and tfPath to find their versions. tfPath may name terraform or tofu; the
// version output tells them apart. It never fails: problems are in each Tool's Status.
func Check(ctx context.Context, r Runner, tgPath, tfPath string) []Tool {
	return []Tool{terragrunt(ctx, r, tgPath), terraform(ctx, r, tfPath)}
}

var (
	semverRe = regexp.MustCompile(`v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)`)
	tfLineRe = regexp.MustCompile(`^(Terraform|OpenTofu) v(\S+)`)
)

func terragrunt(ctx context.Context, r Runner, path string) Tool {
	t := Tool{Tool: KindTerragrunt, Path: path, Minimum: minimum(KindTerragrunt)}
	out, err := run(ctx, r, path, "--version")
	if err != nil {
		return failed(t, err)
	}
	m := semverRe.FindStringSubmatch(out)
	if m == nil {
		return failed(t, fmt.Errorf("no version in the output of %s --version", path))
	}
	return judge(t, m[1])
}

func terraform(ctx context.Context, r Runner, path string) Tool {
	kind := KindTerraform
	if strings.TrimSuffix(filepath.Base(path), ".exe") == string(KindTofu) {
		kind = KindTofu
	}
	t := Tool{Tool: kind, Path: path, Minimum: minimum(kind)}
	// version -json says terraform_version for tofu too, so the first line of the plain output
	// is what tells them apart.
	out, err := run(ctx, r, path, "version")
	if err != nil {
		return failed(t, err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	m := tfLineRe.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return failed(t, fmt.Errorf("no version in the output of %s version", path))
	}
	if m[1] == "OpenTofu" {
		t.Tool = KindTofu
	} else {
		t.Tool = KindTerraform
	}
	t.Minimum = minimum(t.Tool)
	return judge(t, m[2])
}

func run(ctx context.Context, r Runner, path string, args ...string) (string, error) {
	res, err := r.Run(ctx, execx.Cmd{
		Name: path, Args: args, Timeout: timeout,
		// Terraform asks checkpoint.hashicorp.com whether it is outdated unless this is set.
		Env: []string{"CHECKPOINT_DISABLE=1"},
	})
	if err != nil {
		return "", err //nolint:wrapcheck // execx errors name the command
	}
	return string(res.Stdout), nil
}

func failed(t Tool, err error) Tool {
	t.Status, t.Error = StatusError, err.Error()
	if errors.Is(err, execx.ErrNotFound) {
		t.Status, t.Error = StatusNotFound, ""
	}
	return t
}

// judge compares v with the minimum and the gated features. A prerelease counts as its release.
func judge(t Tool, v string) Tool {
	t.Version = v
	got, err := version.NewVersion(v)
	if err != nil {
		return failed(t, fmt.Errorf("parse version %q: %w", v, err))
	}
	core := got.Core()
	t.Status = StatusOK
	if core.LessThan(version.Must(version.NewVersion(t.Minimum))) {
		t.Status = StatusTooOld
	}
	for _, f := range gated(t.Tool) {
		f.Available = f.Needs != "" && !core.LessThan(version.Must(version.NewVersion(f.Needs)))
		t.Features = append(t.Features, f)
	}
	return t
}
