package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/render"
)

func newConfigCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate configuration",
		Long: `Configuration is layered; later layers win:

  defaults < user file < project file < env (TFCENSUS_*) < flags

User file:    $XDG_CONFIG_HOME/tfcensus/config.yaml (~/.config/tfcensus/config.yaml)
Project file: .tfcensus.yaml, found by walking up from the working directory to the git root
Env vars:     TFCENSUS_<KEY> with dots as underscores, e.g. TFCENSUS_CACHE_TTL=30m.
              Use __ to mark nesting explicitly (TFCENSUS_DISCOVERY__GENERATE_STACKS=true).
              Lists are comma-separated.

Unknown keys and wrong types are errors (exit 2), reported with file:line:column.`,
		Args: noArgs,
	}
	cmd.AddCommand(
		newConfigShowCmd(d, g),
		newConfigPathCmd(d, g),
		newConfigValidateCmd(d, g),
	)
	return cmd
}

func newConfigShowCmd(d *app.Deps, g *globals) *cobra.Command {
	var sources bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration",
		Example: `  tfcensus config show
  tfcensus config show --sources
  TFCENSUS_CACHE_TTL=30m tfcensus config show --sources --format json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
				return usageError(err)
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			switch {
			case g.outFormat == render.FormatJSON:
				return writeConfigJSON(d, res, sources)
			case sources:
				return writeConfigSources(d, res)
			default:
				return writeConfigYAML(d, res)
			}
		},
	}
	cmd.Flags().BoolVar(&sources, "sources", false, "show where each value comes from")
	return cmd
}

type configShowData struct {
	Config  map[string]any    `json:"config"`
	Sources map[string]string `json:"sources,omitempty"`
}

func writeConfigJSON(d *app.Deps, res *config.Result, withSources bool) error {
	data := configShowData{Config: res.Raw()}
	if withSources {
		data.Sources = map[string]string{}
		for _, k := range res.Keys() {
			data.Sources[k] = res.Source(k).String()
		}
	}
	return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), data))
}

func writeConfigYAML(d *app.Deps, res *config.Result) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2) //nolint:mnd // two-space YAML indent
	if err := enc.Encode(res.Raw()); err != nil {
		return fmt.Errorf("encode yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode yaml: %w", err)
	}
	if _, err := d.Stdout.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func writeConfigSources(d *app.Deps, res *config.Result) error {
	tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0) //nolint:mnd // column padding
	_, _ = fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
	for _, k := range res.Keys() {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", k, displayValue(res.Value(k)), res.Source(k))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func displayValue(v any) string {
	if s, ok := v.(string); ok {
		if s == "" {
			return `""`
		}
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func newConfigPathCmd(d *app.Deps, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file locations tfcensus reads",
		Args:  noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
				return usageError(err)
			}
			cwd, err := d.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			files, err := config.Locate(config.Options{Environ: d.Env, Cwd: cwd, ExplicitFile: g.configFile})
			if err != nil {
				return err
			}
			if g.outFormat == render.FormatJSON {
				data := map[string]any{"files": files}
				return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), data))
			}
			return writeFiles(d, files)
		},
	}
}

func writeFiles(d *app.Deps, files []config.File) error {
	tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0) //nolint:mnd // column padding
	for _, f := range files {
		path, state := f.Path, "found"
		switch {
		case path == "":
			path, state = "-", "none found"
		case !f.Found:
			state = "not found"
		}
		if f.Explicit {
			state += ", explicit"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Kind, state, path)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write paths: %w", err)
	}
	return nil
}

func newConfigValidateCmd(d *app.Deps, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:               "validate [file...]",
		Short:             "Validate config files against the schema",
		ValidArgsFunction: complete.Files("yaml", "yml"),
		Long: `Without arguments, validates the user and project config files plus TFCENSUS_* env vars and
flags, exactly as other commands would load them. With arguments, validates only those files.

Exits 2 and prints every problem with file:line:column when something is invalid.`,
		Example: `  tfcensus config validate
  tfcensus config validate .tfcensus.yaml ~/.config/tfcensus/config.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
				return usageError(err)
			}
			valid, err := validateConfig(cmd, d, g, args)
			if err != nil {
				return err
			}
			if g.outFormat == render.FormatJSON {
				data := map[string]any{"valid": valid}
				return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), data))
			}
			return writeValid(d, valid)
		},
	}
}

func writeValid(d *app.Deps, valid []string) error {
	var b strings.Builder
	if len(valid) == 0 {
		b.WriteString("no config files found; using defaults\n")
	}
	for _, p := range valid {
		b.WriteString("valid: " + p + "\n")
	}
	if _, err := io.WriteString(d.Stdout, b.String()); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	return nil
}

func validateConfig(cmd *cobra.Command, d *app.Deps, g *globals, args []string) ([]string, error) {
	if len(args) == 0 {
		res, err := g.loadConfig(cmd, d)
		if err != nil {
			return nil, err
		}
		var valid []string
		for _, f := range res.Files {
			if f.Found {
				valid = append(valid, f.Path)
			}
		}
		return valid, nil
	}
	all := &config.ValidationError{}
	for _, path := range args {
		err := config.ValidateFile(path)
		var ve *config.ValidationError
		switch {
		case err == nil:
		case errors.As(err, &ve):
			all.Problems = append(all.Problems, ve.Problems...)
		default:
			all.Problems = append(all.Problems, config.Problem{Origin: path, Message: err.Error()})
		}
	}
	if len(all.Problems) > 0 {
		return nil, all
	}
	return args, nil
}
