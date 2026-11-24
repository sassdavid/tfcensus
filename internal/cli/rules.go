package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/term"
)

const flagResource = "resource"

func newRulesCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Work with ownership rules",
		Long: `Ownership rules sort the resources no unit manages into categories such as aws-managed,
other-iac or derived. The built-in packs run first, then the rules under rules: in the config and
in --rules files. rules test shows which rule matches a sample resource without calling AWS.`,
		Example: `  tfcensus rules test rules.yaml
  tfcensus rules test rules.yaml --resource samples.json --format json`,
		Args: noArgs,
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
			return writeRuleTraces(d, g, eng, samples)
		},
	}
	cmd.Flags().StringVar(&resource, flagResource, "", "JSON file with a resource or an array of resources to evaluate")
	_ = cmd.RegisterFlagCompletionFunc(flagResource, complete.Files("json"))
	cmd.ValidArgsFunction = complete.Files("yaml", "yml")
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
	setRelated(cmd, "guides/rules", "concepts/rules")
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

func writeRuleTraces(d *app.Deps, g *globals, eng *ownership.Engine, samples []ownership.Resource) error {
	traces := make([]ruleTrace, len(samples))
	for i := range samples {
		traces[i] = ruleTrace{Resource: samples[i], Decision: eng.Evaluate(&samples[i], nil, true)}
	}
	if g.outFormat == render.FormatJSON {
		return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), traces))
	}
	var b strings.Builder
	for i := range traces {
		t := &traces[i]
		if i > 0 {
			b.WriteString("\n")
		}
		r := &t.Resource
		head := fmt.Sprintf("resource %d: %s %s", i+1, strings.TrimSpace(r.Type+" "+r.SourceType),
			firstNonEmpty(r.ARN, r.ID, r.Name))
		b.WriteString(term.Heading.Paint(head) + "\n")
		rows := make([][]term.Cell, len(t.Decision.Trace))
		for j, s := range t.Decision.Trace {
			outcome := string(s.Outcome)
			if s.Detail != "" {
				outcome += ": " + s.Detail
			}
			rows[j] = []term.Cell{{Text: s.Rule}, {Text: outcome, Tone: outcomeTone(s.Outcome)}}
		}
		b.WriteString(rowsText(rows))
		dec := &t.Decision
		verdict := "no rule matches (" + ownership.RuleNoMatch + ")"
		tone := term.Plain
		if dec.Matched {
			verdict = fmt.Sprintf("%s: %s (%s)", dec.Category, dec.Reason, dec.Rule)
			if dec.Owner != "" {
				verdict = fmt.Sprintf("%s, owner %s: %s (%s)", dec.Category, dec.Owner, dec.Reason, dec.Rule)
			}
			tone = scan.Category(dec.Category).Tone()
		}
		b.WriteString("  result: " + tone.Paint(verdict) + "\n")
	}
	return writeText(g.textOut(d), b.String(), "rules")
}

// outcomeTone shows a match green, an error red, an expired or never-hide rule yellow, and
// rules that didn't apply faint.
func outcomeTone(o ownership.Outcome) term.Tone {
	switch o {
	case ownership.OutcomeMatch:
		return term.Good
	case ownership.OutcomeError:
		return term.Bad
	case ownership.OutcomeExpired, ownership.OutcomeNeverHide:
		return term.Warn
	case ownership.OutcomeNoMatch, ownership.OutcomePackOff, ownership.OutcomeSkipped:
		return term.Muted
	}
	return term.Plain
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "-"
}
