package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/render"
)

const flagResource = "resource"

func newRulesCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Work with ownership rules",
		Args:  noArgs,
	}
	cmd.AddCommand(newRulesTestCmd(d, g))
	return cmd
}

func newRulesTestCmd(d *app.Deps, g *globals) *cobra.Command {
	var resource string
	cmd := &cobra.Command{
		Use:   "test <rules.yaml>",
		Short: "Compile ownership rules and evaluate them against sample resources, offline",
		Long: `rules test compiles the built-in packs and the rules in a YAML file (a list under rules:, the
same shape as the config key) and reports the first error with the rule name and column.

With --resource, it evaluates every rule against each resource in a JSON file (one object or an
array) and prints the trace: each rule's outcome and the category of the first match. A resource
has the fields a rule sees as resource: account, region, type, source_type, tf_types, arn, id,
name, tags, attrs, layer, sources, parents, relationships and created. scan --explain --format
json prints resources in this shape.

Packs, never_hide and the config's rules come from the loaded config; the file's rules run after
the config's. rules test makes no AWS calls. Expired rules show as expired.`,
		Example: `  tfcensus rules test rules.yaml
  tfcensus rules test rules.yaml --resource sample.json`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError(fmt.Errorf("%s takes one rules file, got %d arguments", cmd.CommandPath(), len(args)))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
				return usageError(err)
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			eng, err := ownershipEngine(d, &res.Config, args, nil)
			if err != nil {
				return err
			}
			for _, name := range eng.Expired() {
				d.Logger.WarnContext(cmd.Context(), "ownership rule expired", "rule", name)
			}
			if resource == "" {
				_, err = fmt.Fprintf(d.Stdout, "%d rules compiled (%s)\n", len(eng.Rules()), args[0])
				if err != nil {
					return fmt.Errorf("write rules: %w", err)
				}
				return nil
			}
			samples, err := readSamples(resource)
			if err != nil {
				return usageError(err)
			}
			return writeRuleTraces(d, g.outFormat, eng, samples)
		},
	}
	cmd.Flags().StringVar(&resource, flagResource, "", "JSON file with a resource or an array of resources to evaluate")
	return cmd
}

// readRulesFile reads the rules under rules: in a YAML file. Unknown keys are errors.
func readRulesFile(path string) ([]ownership.Rule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rules: %w", err)
	}
	var f struct {
		Rules []ownership.Rule `yaml:"rules"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err = dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read rules %s: %w", path, err)
	}
	return f.Rules, nil
}

func readSamples(path string) ([]ownership.Resource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --%s: %w", flagResource, err)
	}
	b = bytes.TrimSpace(b)
	var out []ownership.Resource
	if bytes.HasPrefix(b, []byte("[")) {
		err = json.Unmarshal(b, &out)
	} else {
		var one ownership.Resource
		err = json.Unmarshal(b, &one)
		out = []ownership.Resource{one}
	}
	if err != nil {
		return nil, fmt.Errorf("decode --%s %s: %w", flagResource, path, err)
	}
	for i := range out {
		r := &out[i]
		if r.Tags == nil {
			r.Tags = map[string]string{}
		}
		if r.Attrs == nil {
			r.Attrs = map[string]string{}
		}
	}
	return out, nil
}

type ruleTrace struct {
	Resource ownership.Resource `json:"resource"`
	Decision ownership.Decision `json:"decision"`
}

func writeRuleTraces(d *app.Deps, format render.Format, eng *ownership.Engine, samples []ownership.Resource) error {
	traces := make([]ruleTrace, len(samples))
	for i := range samples {
		traces[i] = ruleTrace{Resource: samples[i], Decision: eng.Evaluate(&samples[i], nil, true)}
	}
	if format == render.FormatJSON {
		return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), traces))
	}
	w := d.Stdout
	for i := range traces {
		t := &traces[i]
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		r := &t.Resource
		_, _ = fmt.Fprintf(
			w,
			"resource %d: %s %s\n",
			i+1,
			strings.TrimSpace(r.Type+" "+r.SourceType),
			firstNonEmpty(r.ARN, r.ID, r.Name),
		)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
		for _, s := range t.Decision.Trace {
			outcome := string(s.Outcome)
			if s.Detail != "" {
				outcome += ": " + s.Detail
			}
			_, _ = fmt.Fprintf(tw, "  %s\t%s\n", s.Rule, outcome)
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("write rules: %w", err)
		}
		dec := &t.Decision
		verdict := "no rule matches (" + ownership.RuleNoMatch + ")"
		if dec.Matched {
			verdict = fmt.Sprintf("%s: %s (%s)", dec.Category, dec.Reason, dec.Rule)
			if dec.Owner != "" {
				verdict = fmt.Sprintf("%s, owner %s: %s (%s)", dec.Category, dec.Owner, dec.Reason, dec.Rule)
			}
		}
		if _, err := fmt.Fprintf(w, "  result: %s\n", verdict); err != nil {
			return fmt.Errorf("write rules: %w", err)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "-"
}
