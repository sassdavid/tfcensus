package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/docsurl"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/term"
)

func newConfigCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate configuration",
		Long: `Configuration is layered; later layers win:

  defaults < user file < project file < env (TFCENSUS_*) < flags

User file:    $XDG_CONFIG_HOME/tfcensus/config.yaml (~/.config/tfcensus/config.yaml)
Project file: .tfcensus.yaml, found by walking up from the working directory to the git root
Env vars:     TFCENSUS_<KEY> with dots and dashes as underscores, e.g. TFCENSUS_CACHE_TTL=30m.
              Use __ to mark nesting explicitly (TFCENSUS_DISCOVERY__GENERATE_STACKS=true).
              Only scalars and string lists can be set; lists are comma-separated.
              Maps and lists of objects need a file. An unknown TFCENSUS_* variable is
              ignored with a warning.

Unknown keys and wrong types are errors (exit 2), reported with file:line:column.
'tfcensus config init' writes a commented file to start from. Every key, its default and
its variable are listed at ` + docsurl.Page("reference/config") + `.`,
		Example: `  tfcensus config init
  tfcensus config show --sources
  tfcensus config validate .tfcensus.yaml`,
		Args: noArgs,
	}
	cmd.AddCommand(
		newConfigShowCmd(d, g),
		newConfigPathCmd(d, g),
		newConfigValidateCmd(d, g),
		newConfigInitCmd(d),
	)
	setRelated(cmd, "reference/config", "reference/environment")
	return cmd
}

func newConfigShowCmd(d *app.Deps, g *globals) *cobra.Command {
	var sources bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration",
		Long: `show merges the defaults, the user file, the project file, TFCENSUS_* variables and flags,
and prints the result. --sources adds the layer each value came from, and for env and flag values
the variable or flag that set it.`,
		Example: `  tfcensus config show
  tfcensus config show --sources
  TFCENSUS_CACHE_TTL=30m tfcensus config show --sources --format json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
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
	var rows [][]string
	for _, k := range res.Keys() {
		rows = append(rows, []string{k, displayValue(res.Value(k)), res.Source(k).String()})
	}
	return writeText(d.Stdout, term.Table([]string{"KEY", "VALUE", colSource}, term.Rows(rows)), "config")
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
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the config file locations tfcensus reads",
		Long: `path prints the user file and the project file that tfcensus would read from the working
directory, and whether each one exists. --config or TFCENSUS_CONFIG replaces the project file.`,
		Example: `  tfcensus config path
  tfcensus config path --config ci/tfcensus.yaml --format json`,
		Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
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
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
	return cmd
}

func writeFiles(d *app.Deps, files []config.File) error {
	var rows [][]string
	for _, f := range files {
		path, state := f.Path, "found"
		switch {
		case path == "":
			path, state = "-", "none found"
		case !f.Found:
			state = cellNotFound
		}
		if f.Explicit {
			state += ", explicit"
		}
		rows = append(rows, []string{string(f.Kind), state, path})
	}
	return writeText(d.Stdout, term.Table(nil, term.Rows(rows)), "paths")
}

func newConfigValidateCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "validate [file...]",
		Short:             "Validate config files against the schema",
		ValidArgsFunction: complete.Files("yaml", "yml"),
		Long: `Without arguments, validates the user and project config files plus TFCENSUS_* env vars and
flags, exactly as other commands would load them. With arguments, validates only those files.

Exits 2 and prints every problem with file:line:column when something is invalid.`,
		Example: `  tfcensus config validate
  tfcensus config validate .tfcensus.yaml ~/.config/tfcensus/config.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
	return cmd
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

func newConfigInitCmd(d *app.Deps) *cobra.Command {
	var force, user bool
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Write a commented config file",
		Long: `Writes a config file that lists every key commented out at its default, so the file
changes nothing until you uncomment a key. It goes to ` + config.ProjectFileName + ` in the working
directory, to path, or with --user to the user file. The directory is created when it's missing.

An existing file is kept, with exit code 2, unless you pass --force. The written path goes
to stderr.`,
		Example: `  ` + config.AppName + ` config init
  ` + config.AppName + ` config init --user
  ` + config.AppName + ` config init ci/` + config.AppName + `.yaml --force`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return usageError(err)
			}
			return nil
		},
		ValidArgsFunction: complete.Files("yaml", "yml"),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := initPath(d, args, user)
			if err != nil {
				return err
			}
			err = config.WriteTemplate(path, force)
			if errors.Is(err, config.ErrExists) {
				return usageError(fmt.Errorf("%w; pass --force to replace it", err))
			}
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(d.Stderr, "%s: wrote %s\n", config.AppName, path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing file")
	cmd.Flags().BoolVar(&user, "user", false, "write the user file instead of "+config.ProjectFileName)
	setFormats(cmd)
	setExitCodes(cmd)
	return cmd
}

// initPath returns where config init writes: the argument, the user file or the project file
// in the working directory.
func initPath(d *app.Deps, args []string, user bool) (string, error) {
	switch {
	case user && len(args) > 0:
		return "", usageError(errors.New("--user and a path can't be combined"))
	case user:
		return config.UserConfigPath(d.Env.Lookup, runtime.GOOS)
	}
	cwd, err := d.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	if len(args) == 0 {
		return filepath.Join(cwd, config.ProjectFileName), nil
	}
	if filepath.IsAbs(args[0]) {
		return args[0], nil
	}
	return filepath.Join(cwd, args[0]), nil
}
