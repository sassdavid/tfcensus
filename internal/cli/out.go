package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/render"
)

const (
	flagOut        = "out"
	flagMDMaxChars = "md-max-chars"
)

// addOutputFlags adds --out and --md-max-chars to a command that writes a report in formats.
func addOutputFlags(cmd *cobra.Command, g *globals, formats ...render.Format) {
	choices := outChoices(formats)
	f := cmd.Flags()
	f.StringArrayVar(&g.outSpecs, flagOut, nil, complete.Usage(
		"also write the report to a file as format=path (repeatable); format is one of {values}", choices))
	f.IntVar(&g.mdMaxChars, flagMDMaxChars, render.DefaultMDMaxChars,
		"cut Markdown output to this many characters at a row or section boundary; 0 means no limit")
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagOut:        complete.Assign(choices),
		flagMDMaxChars: complete.NoFiles,
	})
}

// outChoices lists the formats of the command that --out accepts.
func outChoices(formats []render.Format) []complete.Choice {
	return slices.DeleteFunc(formatChoices(), func(c complete.Choice) bool {
		f := render.Format(c.Value)
		return !slices.Contains(render.OutFormats(), f) || !slices.Contains(formats, f)
	})
}

// checkFormats checks --format and every --out format against allowed, and parses --out. Its
// errors are usage errors.
func (g *globals) checkFormats(allowed ...render.Format) error {
	if err := g.outFormat.Check(allowed...); err != nil {
		return err //nolint:wrapcheck // the caller wraps it as a usage error
	}
	outs, err := render.ParseOutputs(g.outSpecs)
	if err != nil {
		return err //nolint:wrapcheck // the caller wraps it as a usage error
	}
	for _, o := range outs {
		if err = o.Format.Check(allowed...); err != nil {
			return fmt.Errorf("--%s %s=%s: %w", flagOut, o.Format, o.Path, err)
		}
	}
	if g.mdMaxChars < 0 {
		return fmt.Errorf("--%s must be 0 or more, got %d", flagMDMaxChars, g.mdMaxChars)
	}
	g.outputs = outs
	return nil
}

// sink sends the command's report to stdout and to the --out files.
func (g *globals) sink(cmd *cobra.Command, d *app.Deps) render.Sink {
	return render.Sink{
		Stdout:     d.Stdout,
		Format:     g.outFormat,
		Outputs:    g.outputs,
		MDMaxChars: g.mdMaxChars,
		Command:    cmd.CommandPath(),
	}
}
