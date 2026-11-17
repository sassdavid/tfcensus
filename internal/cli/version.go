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
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Long: `version prints the version, the commit and date it was built from, the Go version and the
platform. With --format json it writes the JSON envelope that every report uses.`,
		Example: `  tfcensus version
  tfcensus version --format json`,
		Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return writeVersion(d, g.outFormat, buildinfo.Get())
		},
	}
	setFormats(cmd, textFormats()...)
	setExitCodes(cmd)
	return cmd
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
