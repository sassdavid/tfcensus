package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/render"
)

const (
	flagTFPath = "tf-path"
	flagTGPath = "tg-path"
)

func flagKeys() map[string]string {
	return map[string]string{
		flagTFPath:            "binaries.terraform",
		flagTGPath:            "binaries.terragrunt",
		flagGenerateStacks:    "discovery.generate_stacks",
		flagRender:            "discovery.render",
		flagProfile:           "accounts.profiles",
		flagOrg:               "accounts.org.enabled",
		flagOrgRole:           "accounts.org.role",
		flagManagementProfile: "accounts.org.management_profile",
		flagRegion:            "regions",
		flagDiscoverPattern:   "audit.discover_patterns",
		flagLayers:            "scan.layers",
		flagMode:              "drift.mode",
		flagCollapse:          "drift.collapse",
	}
}

// globals holds the persistent flags and the state derived from them.
type globals struct {
	format     string
	configFile string
	verbose    int
	quiet      bool
	logFormat  string
	logFile    string
	color      string
	noInput    bool
	tfPath     string
	tgPath     string

	outSpecs   []string // --out, on commands that write a report
	mdMaxChars int

	outFormat render.Format
	outputs   []render.Output
	started   bool
	closers   []io.Closer
	mcpServe  serveFunc // tests serve the MCP server in-process
}

func (g *globals) close() {
	for _, c := range g.closers {
		_ = c.Close()
	}
	g.closers = nil
}

// NewRootCmd returns the full command tree. scripts/gendocs uses it.
func NewRootCmd(d *app.Deps) *cobra.Command {
	root, _ := newRoot(d)
	root.InitDefaultHelpCmd()
	return root
}

func newRoot(d *app.Deps) (*cobra.Command, *globals) {
	g := &globals{}
	def := config.Default()
	root := &cobra.Command{
		Use:   config.AppName,
		Short: "Take a census of Terraform/OpenTofu and Terragrunt fleets on AWS",
		Long: `tfcensus lists Terraform/OpenTofu and Terragrunt units and their state, finds AWS
resources that no unit manages, and audits state backends. It is read-only by default.

stdout carries data, stderr carries logs. Use --format json for scripting.`,
		SilenceErrors:     true,
		SilenceUsage:      true,
		DisableAutoGenTag: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return g.setup(cmd, d)
		},
	}
	root.SetIn(d.Stdin)
	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })

	pf := root.PersistentFlags()
	pf.StringVar(&g.format, "format", "", complete.Usage(
		"output format: {values} (default tty when stdout is a terminal, else plain)", formatChoices()))
	pf.StringVar(&g.configFile, "config", "",
		"config file to use instead of the discovered "+config.ProjectFileName+" (env "+config.EnvConfigFile+")")
	pf.CountVarP(&g.verbose, "verbose", "v", "more logging: -v info, -vv debug, -vvv trace")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "log errors only")
	pf.StringVar(&g.logFormat, "log-format", string(app.LogFormatText),
		complete.Usage("log format: {values}", logFormatChoices()))
	pf.StringVar(&g.logFile, "log-file", "", "write logs to this file instead of stderr")
	pf.StringVar(&g.color, "color", string(render.ColorAuto),
		complete.Usage("colorize output: {values}", colorChoices()))
	pf.BoolVar(&g.noInput, "no-input", false, "never prompt for input")
	pf.StringVar(&g.tfPath, flagTFPath, def.Binaries.Terraform,
		"terraform or tofu binary, passed to terragrunt as --tf-path (config binaries.terraform)")
	pf.StringVar(&g.tgPath, flagTGPath, def.Binaries.Terragrunt,
		"terragrunt binary (config binaries.terragrunt)")
	registerCompletions(root, map[string]cobra.CompletionFunc{
		"format":     complete.Enum(formatChoices()),
		"config":     complete.Files("yaml", "yml"),
		"log-format": complete.Enum(logFormatChoices()),
		"log-file":   complete.Files(),
		"color":      complete.Enum(colorChoices()),
		flagTFPath:   complete.Files(),
		flagTGPath:   complete.Files(),
	})
	root.MarkFlagsMutuallyExclusive("verbose", "quiet")

	root.AddCommand(
		newVersionCmd(d, g),
		newConfigCmd(d, g),
		newUnitsCmd(d, g),
		newDoctorCmd(d, g),
		newInventoryCmd(d, g),
		newAuditCmd(d, g),
		newScanCmd(d, g),
		newRulesCmd(d, g),
		newDriftCmd(d, g),
		newWhoisCmd(d, g),
		newCodifyCmd(d, g),
		newMoveCmd(d, g),
		newReportCmd(d, g),
		newNotifyCmd(d, g),
		newMCPCmd(d, g),
	)
	addCompletionShells(root)
	defaultNoFiles(root)
	return root, g
}

func (g *globals) setup(cmd *cobra.Command, d *app.Deps) error {
	// Completion requests get unparsed flags here and may print nothing but results.
	if complete.IsRequest(cmd) {
		d.Logger = slog.New(slog.DiscardHandler)
		return nil
	}
	// cobra checks these only after PersistentPreRunE; check them first so they exit 2.
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return usageError(err)
	}
	if err := cmd.ValidateFlagGroups(); err != nil {
		return usageError(err)
	}
	format, err := render.ParseFormat(g.format)
	if err != nil {
		return usageError(err)
	}
	colorMode, err := render.ParseColorMode(g.color)
	if err != nil {
		return usageError(err)
	}
	logFormat, err := app.ParseLogFormat(g.logFormat)
	if err != nil {
		return usageError(err)
	}

	logW, logTTY := d.Stderr, app.IsTerminal(d.Stderr)
	if g.logFile != "" {
		f, openErr := os.OpenFile(g.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if openErr != nil {
			return usageError(fmt.Errorf("open log file: %w", openErr))
		}
		g.closers = append(g.closers, f)
		logW, logTTY = f, false
	}
	d.Logger = app.NewLogger(logW, app.LogOptions{
		Format:  logFormat,
		Verbose: g.verbose,
		Quiet:   g.quiet,
		Color:   render.UseColor(colorMode, d.Env.Lookup, logTTY) && app.EnableVirtualTerminal(logW),
	})
	g.outFormat = format.Resolve(app.IsTerminal(d.Stdout))
	g.started = true
	d.Logger.DebugContext(cmd.Context(), "starting", "command", cmd.CommandPath(), "format", string(g.outFormat))
	return nil
}

func (g *globals) loadConfig(cmd *cobra.Command, d *app.Deps) (*config.Result, error) {
	cwd, err := d.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	res, err := config.Load(config.Options{
		Environ:      d.Env,
		Cwd:          cwd,
		ExplicitFile: g.configFile,
		Flags:        cmd.Flags(),
		FlagKeys:     flagKeys(),
	})
	if err != nil {
		return nil, err
	}
	for _, name := range res.UnknownEnv {
		d.Logger.WarnContext(cmd.Context(), "ignoring unknown environment variable", "name", name)
	}
	for _, f := range res.Files {
		if f.Found {
			d.Logger.DebugContext(cmd.Context(), "loaded config file", "kind", string(f.Kind), "path", f.Path)
		}
	}
	d.Config = res
	return res, nil
}

func formatChoices() []complete.Choice {
	return []complete.Choice{
		{Value: string(render.FormatTTY), Desc: "table with color for a terminal"},
		{Value: string(render.FormatPlain), Desc: "table without color"},
		{Value: string(render.FormatMD), Desc: "Markdown"},
		{Value: string(render.FormatJSON), Desc: "JSON envelope for scripts"},
		{Value: string(render.FormatCSV), Desc: "comma-separated values"},
		{Value: string(render.FormatSARIF), Desc: "SARIF 2.1.0 for code scanning"},
		{Value: string(render.FormatGitHub), Desc: "GitHub Actions annotations"},
	}
}

// noArgs is cobra.NoArgs with exit code 2.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageError(fmt.Errorf("%s takes no arguments, got %q", cmd.CommandPath(), strings.Join(args, " ")))
	}
	return nil
}
