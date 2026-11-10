package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/render"
)

func newVersionCmd(d *app.Deps, g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Example: `  tfcensus version
  tfcensus version --format json`,
		Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
				return usageError(err)
			}
			return writeVersion(d, g.outFormat, buildinfo.Get())
		},
	}
}

func writeVersion(d *app.Deps, format render.Format, info buildinfo.Info) error {
	if format == render.FormatJSON {
		return render.JSON(d.Stdout, render.NewEnvelope(info.Version, d.Clock(), info))
	}
	tw := tabwriter.NewWriter(d.Stdout, 0, 0, 1, ' ', 0)
	_, _ = fmt.Fprintf(tw, "tfcensus\t%s\n", info.Version)
	_, _ = fmt.Fprintf(tw, "commit\t%s\n", info.Commit)
	_, _ = fmt.Fprintf(tw, "built\t%s\n", info.Date)
	_, _ = fmt.Fprintf(tw, "go\t%s\n", info.GoVersion)
	_, _ = fmt.Fprintf(tw, "platform\t%s\n", info.Platform)
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	return nil
}
