package discovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/sassdavid/tfcensus/internal/execx"
)

// findItem is one entry of terragrunt find --json. The find format is covered by terragrunt's
// 1.x compatibility promise.
type findItem struct {
	Type         string       `json:"type"`
	Path         string       `json:"path"`
	Dependencies []string     `json:"dependencies"`
	Exclude      *findExclude `json:"exclude"`
}

type findExclude struct {
	If      *bool    `json:"if"`
	Actions []string `json:"actions"`
}

// renderOutput is the part of terragrunt render --json that tfcensus reads. render isn't covered
// by terragrunt's compatibility promise, so unknown fields are ignored and missing ones are
// reported, not fatal.
type renderOutput struct {
	RemoteState *struct {
		Backend string         `json:"backend"`
		Config  map[string]any `json:"config"`
	} `json:"remote_state"`
	Generate map[string]struct {
		Path     string `json:"path"`
		Contents string `json:"contents"`
		Disable  bool   `json:"disable"`
	} `json:"generate"`
	Inputs map[string]any `json:"inputs"`
}

// logFlags keep terragrunt's stderr to errors, which become the reasons for unresolved units.
func logFlags() []string { return []string{"--no-color", "--log-level", "error"} }

// tgEnv passes the terraform binary to subcommands such as find that reject --tf-path.
func (d *Discoverer) tgEnv() []string { return []string{"TG_TF_PATH=" + d.opts.Terraform} }

func (d *Discoverer) find(ctx context.Context, dir string) ([]findItem, error) {
	args := append([]string{"find", "--json", "--dag", "--dependencies", "--exclude"}, logFlags()...)
	for _, f := range d.opts.Filters {
		args = append(args, "--filter", f)
	}
	res, err := d.runner.Run(ctx, execx.Cmd{Name: d.opts.Terragrunt, Args: args, Dir: dir, Env: d.tgEnv()})
	if err != nil {
		return nil, tgError("terragrunt find", res, err)
	}
	var items []findItem
	if err = json.Unmarshal(res.Stdout, &items); err != nil {
		return nil, fmt.Errorf("parse terragrunt find output: %w", err)
	}
	return items, nil
}

func (d *Discoverer) generateStacks(ctx context.Context, dir string) error {
	args := append([]string{"stack", "generate"}, logFlags()...)
	res, err := d.runner.Run(ctx, execx.Cmd{Name: d.opts.Terragrunt, Args: args, Dir: dir, Env: d.tgEnv()})
	if err != nil {
		return tgError("terragrunt stack generate", res, err)
	}
	return nil
}

// render runs terragrunt render --json for one unit. --no-dependency-outputs keeps terragrunt from
// reading other units' state to fill dependency outputs, which tfcensus never needs.
func (d *Discoverer) render(ctx context.Context, dir, unit string) (*renderOutput, error) {
	args := append([]string{
		"render", "--working-dir", unit, "--json", "--no-dependency-outputs", "--tf-path", d.opts.Terraform,
	}, logFlags()...)
	res, err := d.runner.Run(ctx, execx.Cmd{
		Name: d.opts.Terragrunt, Args: args, Dir: dir, Env: d.tgEnv(), Timeout: d.opts.RenderTimeout,
	})
	if err != nil {
		return nil, tgError("render", res, err)
	}
	var out renderOutput
	if err = json.Unmarshal(res.Stdout, &out); err != nil {
		return nil, fmt.Errorf("parse render output: %w", err)
	}
	return &out, nil
}

// tgError keeps the line of terragrunt's stderr that explains a failure.
func tgError(what string, res *execx.Result, err error) error {
	if _, ok := errors.AsType[*execx.ExitError](err); ok && res != nil {
		if line := stderrSummary(res.Stderr); line != "" {
			return fmt.Errorf("%s failed: %s", what, line)
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

// stderrSummary returns terragrunt's aggregated error ("* file:line: message"), or else its last
// log line, without the timestamp and level.
func stderrSummary(stderr []byte) string {
	prefix := regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d+\s+[A-Z]+\s+`)
	var last string
	sc := bufio.NewScanner(bytes.NewReader(stderr))
	for sc.Scan() {
		line := strings.TrimSpace(prefix.ReplaceAllString(sc.Text(), ""))
		if rest, ok := strings.CutPrefix(line, "* "); ok {
			return rest
		}
		if line != "" {
			last = line
		}
	}
	return last
}

func (out *renderOutput) unitConfig() (*unitConfig, error) {
	if out.RemoteState == nil || out.RemoteState.Backend == "" {
		return nil, errNoRemoteState
	}
	cfg := &unitConfig{
		backend:   out.RemoteState.Backend,
		config:    out.RemoteState.Config,
		generated: map[string]generated{},
		inputs:    out.Inputs,
	}
	for name, g := range out.Generate {
		if !g.Disable {
			cfg.generated[name] = generated{path: g.Path, contents: g.Contents}
		}
	}
	return cfg, nil
}

// providerFromGenerated reads the aws provider from generated .tf files, where Terragrunt repos
// usually define it.
func providerFromGenerated(gens map[string]generated) providerTarget {
	for _, name := range sortedKeys(gens) {
		g := gens[name]
		if !strings.HasSuffix(g.path, ".tf") {
			continue
		}
		f, diags := hclsyntax.ParseConfig([]byte(g.contents), g.path, hcl.InitialPos)
		if diags.HasErrors() {
			continue
		}
		if p := providerFromBody(f.Body, nil); p.account.value != "" || p.region.value != "" {
			return p
		}
	}
	return providerTarget{}
}

func excluded(e *findExclude) bool {
	return e != nil && e.If != nil && *e.If
}

func excludeWarning(e *findExclude) string {
	actions := "all"
	if len(e.Actions) > 0 {
		actions = strings.Join(slices.Sorted(slices.Values(e.Actions)), ", ")
	}
	return "excluded by terragrunt for: " + actions
}

func stackGenerated(stack string, items []findItem) bool {
	prefix := stack + "/" + generatedStacks + "/"
	return slices.ContainsFunc(items, func(it findItem) bool {
		return it.Type == "unit" && strings.HasPrefix(filepath.ToSlash(it.Path), prefix)
	})
}
