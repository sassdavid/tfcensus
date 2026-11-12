package codify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-version"

	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// Engine is the tool that will run the import blocks.
type Engine string

// Engines.
const (
	EngineTerraform Engine = "terraform"
	EngineOpenTofu  Engine = "opentofu"
)

// awsSource is the aws provider's normalized source in a lock file.
const awsSource = "hashicorp/aws"

// First releases with each feature.
const (
	minIdentity         = "1.12.0" // import identity, in terraform and in tofu
	minQuery            = "1.14.0" // terraform query and list blocks
	minProviderIdentity = "6.0.0"  // resource identity in the aws provider
)

// atLeast reports whether v, without prerelease, is minimum or later.
func atLeast(v *version.Version, minimum string) bool {
	return v != nil && !v.Core().LessThan(version.Must(version.NewVersion(minimum)))
}

// Target is the engine and versions the import blocks are written for.
type Target struct {
	Engine   Engine           `json:"engine,omitempty"`
	Version  *version.Version `json:"-"`
	Provider *version.Version `json:"-"` // the aws provider in the lock file; nil without one
	Source   string           `json:"source"`
}

// MarshalJSON writes the versions as strings.
func (t Target) MarshalJSON() ([]byte, error) {
	type out struct {
		Engine   Engine `json:"engine,omitempty"`
		Version  string `json:"version,omitempty"`
		Provider string `json:"aws_provider,omitempty"`
		Source   string `json:"source"`
	}
	o := out{Engine: t.Engine, Source: t.Source}
	if t.Version != nil {
		o.Version = t.Version.String()
	}
	if t.Provider != nil {
		o.Provider = t.Provider.String()
	}
	b, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("encode target: %w", err)
	}
	return b, nil
}

// String returns "terraform 1.12.2", or "unknown".
func (t Target) String() string {
	if t.Engine == "" || t.Version == nil {
		return "unknown"
	}
	return string(t.Engine) + " " + t.Version.String()
}

// ErrTarget is wrapped by every --target parse error.
var ErrTarget = errors.New("invalid target")

// ParseTarget parses terraform@1.12 or opentofu@1.11.2.
func ParseTarget(s string) (Target, error) {
	name, v, ok := strings.Cut(s, "@")
	if !ok {
		return Target{}, fmt.Errorf("%w %q: want terraform@<version> or opentofu@<version>", ErrTarget, s)
	}
	t := Target{Source: "--target"}
	switch name {
	case "terraform":
		t.Engine = EngineTerraform
	case "opentofu", "tofu":
		t.Engine = EngineOpenTofu
	default:
		return Target{}, fmt.Errorf("%w %q: engine %q: want terraform or opentofu", ErrTarget, s, name)
	}
	ver, err := version.NewVersion(v)
	if err != nil {
		return Target{}, fmt.Errorf("%w %q: %w", ErrTarget, s, err)
	}
	t.Version = ver
	return t, nil
}

// IdentityEngine reports whether the engine supports identity in import blocks.
func (t Target) IdentityEngine() bool { return t.Engine != "" && atLeast(t.Version, minIdentity) }

// IdentityProvider reports whether the locked aws provider has resource identity. Without a lock
// file the provider init installs is current, so it does.
func (t Target) IdentityProvider() bool {
	return t.Provider == nil || atLeast(t.Provider, minProviderIdentity)
}

// Query reports whether the engine has terraform query.
func (t Target) Query() bool { return t.Engine == EngineTerraform && atLeast(t.Version, minQuery) }

// Runner runs a command, as [execx.Runner] does.
type Runner interface {
	Run(ctx context.Context, c execx.Cmd) (*execx.Result, error)
}

// Detect finds the target of the unit in dir. The engine comes from the lock file's registry
// host, else from the binary's name; the version from `<binary> version -json`. The aws
// provider version comes from the lock file. A failed detection leaves the engine or version
// empty and returns why.
func Detect(ctx context.Context, run Runner, binary, dir string) (Target, error) {
	t := Target{Source: "detected"}
	locked, err := tfstate.ReadLockProviders(dir)
	if err != nil {
		return t, err
	}
	binEngine := EngineTerraform
	if strings.Contains(strings.ToLower(filepath.Base(binary)), "tofu") {
		binEngine = EngineOpenTofu
	}
	t.Engine = binEngine
	if p, ok := locked[awsSource]; ok {
		if p.OpenTofu() {
			t.Engine = EngineOpenTofu
		} else {
			t.Engine = EngineTerraform
		}
		if v, verr := version.NewVersion(p.Version); verr == nil {
			t.Provider = v
		}
	}
	if t.Engine != binEngine {
		return t, fmt.Errorf("the lock file is from %s but %s runs %s", t.Engine, binary, binEngine)
	}
	res, err := run.Run(ctx, execx.Cmd{Name: binary, Args: []string{"version", "-json"}, Dir: dir})
	if err != nil {
		return t, fmt.Errorf("run %s version: %w", binary, err)
	}
	var out struct {
		Version string `json:"terraform_version"`
	}
	if err = json.Unmarshal(res.Stdout, &out); err != nil {
		return t, fmt.Errorf("read %s version: %w", binary, err)
	}
	if t.Version, err = version.NewVersion(out.Version); err != nil {
		return t, fmt.Errorf("read %s version: %w", binary, err)
	}
	return t, nil
}
