package cli

import (
	"fmt"
	"io"

	"github.com/carapace-sh/carapace"
	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/render"
)

// carapaceShell is a shell whose completion script comes from carapace, because cobra has none.
type carapaceShell struct {
	name     string // completion subcommand
	carapace string // carapace's name for the shell
	setup    string // how to load the script, for the help text
}

func carapaceShells() []carapaceShell {
	return []carapaceShell{
		{"nushell", "nushell", `The script defines $tfcensus_completer. Save it, source it from config.nu and use it as
the external completer (or call it from yours when the first span is tfcensus):

  tfcensus completion nushell | save --force ~/.config/nushell/tfcensus.nu

  # config.nu
  source ~/.config/nushell/tfcensus.nu
  $env.config.completions.external = {enable: true, completer: $tfcensus_completer}`},
		{"elvish", "elvish", `Add this line to ~/.config/elvish/rc.elv:

  eval (tfcensus completion elvish | slurp)`},
		{"xonsh", "xonsh", `Add this line to ~/.xonshrc:

  exec($(tfcensus completion xonsh))`},
		{"tcsh", "tcsh", "Add this line to ~/.tcshrc:\n\n  eval `tfcensus completion tcsh`"},
		{"oil", "oil", `Add this line to your oils startup file (~/.config/oils/oshrc or yshrc):

  source <(tfcensus completion oil)`},
		{"clink", "cmd-clink", `Save the script as a Lua file in a clink scripts directory, such as
%LOCALAPPDATA%\clink\tfcensus.lua:

  tfcensus completion clink > %LOCALAPPDATA%\clink\tfcensus.lua`},
	}
}

// addCompletionShells creates cobra's completion command and adds one subcommand per carapace
// shell. Their scripts call back into the hidden _carapace command that [enableCarapace] adds.
func addCompletionShells(root *cobra.Command) {
	root.InitDefaultCompletionCmd()
	var comp *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			comp = c
		}
	}
	if comp == nil {
		return
	}
	for _, sh := range carapaceShells() {
		comp.AddCommand(&cobra.Command{
			Use:   sh.name,
			Short: "Generate the autocompletion script for " + sh.name,
			Long: fmt.Sprintf("Generate the autocompletion script for %s. carapace, built into %s, writes it.\n\n%s\n",
				sh.name, root.Name(), sh.setup),
			Args:              noArgs,
			ValidArgsFunction: complete.NoFiles,
			RunE: func(cmd *cobra.Command, _ []string) error {
				// Gen is a no-op when enableCarapace already ran for this tree.
				snippet, err := carapace.Gen(cmd.Root()).Snippet(sh.carapace)
				if err != nil {
					return fmt.Errorf("generate %s completion: %w", sh.name, err)
				}
				_, err = io.WriteString(cmd.OutOrStdout(), snippet+"\n")
				return err //nolint:wrapcheck // a write error on stdout needs no context
			},
		})
	}
}

// defaultNoFiles gives every command without argument completion one that offers nothing.
// Without it, shells offer file names for commands that take no arguments.
func defaultNoFiles(cmd *cobra.Command) {
	if cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0 {
		cmd.ValidArgsFunction = complete.NoFiles
	}
	for _, c := range cmd.Commands() {
		defaultNoFiles(c)
	}
}

// enableCarapace adds the hidden _carapace command. carapace.Gen changes package-level state
// in cobra and carapace, so only a process entry point calls it, never a constructor that
// parallel tests share.
func enableCarapace(root *cobra.Command) {
	carapace.Gen(root)
}

func logFormatChoices() []complete.Choice {
	return []complete.Choice{
		{Value: string(app.LogFormatText), Desc: "human-readable lines"},
		{Value: string(app.LogFormatJSON), Desc: "one JSON object per line"},
	}
}

func colorChoices() []complete.Choice {
	return []complete.Choice{
		{Value: string(render.ColorAuto), Desc: "color on a terminal unless NO_COLOR is set"},
		{Value: string(render.ColorAlways), Desc: "always color"},
		{Value: string(render.ColorNever), Desc: "never color"},
	}
}

// registerCompletions registers a completion function for each named flag of cmd. A wrong name
// leaves the flag without one, which TestCompletionCoverage reports.
func registerCompletions(cmd *cobra.Command, funcs map[string]cobra.CompletionFunc) {
	for name, f := range funcs {
		_ = cmd.RegisterFlagCompletionFunc(name, f)
	}
}
