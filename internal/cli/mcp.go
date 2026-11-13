package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/mcpkit"
	"github.com/sassdavid/tfcensus/internal/mcpserver"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/report"
)

// serveFunc serves an MCP server until the client is done.
type serveFunc func(ctx context.Context, s *mcpkit.Server) error

type mcpFlags struct {
	index, scan, audit, drift string
	maxBytes                  int
}

func newMCPCmd(d *app.Deps, g *globals) *cobra.Command {
	var f mcpFlags
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve units, inventory, whois, scan_summary, audit_findings and drift_summary to an MCP client over stdio",
		Long: `mcp runs a Model Context Protocol server on stdin and stdout, for agents such as Claude Code.
Logs go to stderr. Each tool returns the JSON envelope the matching command prints with
--format json:

  units           tfcensus report units --index <file>
  inventory       tfcensus report inventory --index <file>
  whois           tfcensus whois --index <file> <arn|id>... (no --live, no accounts)
  scan_summary    tfcensus report scan [--from <file>]
  audit_findings  tfcensus report audit --from <file>
  drift_summary   tfcensus report drift --from <file>

The tools answer from saved files only. They never call AWS, start terragrunt or terraform,
read a state, run a plan, or write a file, and move apply, rollback and mark-applied are not
tools. Run the commands that collect (inventory --save-index, scan, audit, drift) yourself,
and pass their output with the flags below. A tool whose file wasn't given fails with a
message naming the flag. scan_summary without --scan reads the newest scan cache snapshot and
applies the config's ownership rules; a snapshot older than cache.ttl comes with a warning.
The files are read again on every call, so a new run is picked up without a restart.

A result over --max-result-bytes (default 50,000, Claude Code's limit for showing a result
to the model) is split. The envelope then has a page field, and the agent passes
page.next_cursor back to the same tool for the rest. Double-managed and unmanaged resources
and high-severity findings come first. A cursor lasts 15 minutes after its last use.`,
		Example: `  tfcensus inventory --save-index ~/.cache/tfcensus/index.json.zst
  claude mcp add --transport stdio --scope local tfcensus -- tfcensus mcp --index ~/.cache/tfcensus/index.json.zst
  tfcensus mcp --index index.json.zst --scan scan.json --audit audit.json --drift drift.json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			serve := g.mcpServe
			if serve == nil {
				serve = func(ctx context.Context, s *mcpkit.Server) error { return s.Run(ctx, d.Stdin, d.Stdout) }
			}
			return runMCP(cmd, d, g, f, serve)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(
		&f.index,
		flagIndex,
		"",
		"state index from tfcensus inventory --save-index, for units, inventory and whois",
	)
	fl.StringVar(&f.scan, flagScan, "", "scan JSON for scan_summary (default: the newest scan cache snapshot)")
	fl.StringVar(&f.audit, flagAudit, "", "audit JSON for audit_findings")
	fl.StringVar(&f.drift, flagDrift, "", "drift JSON for drift_summary")
	fl.IntVar(&f.maxBytes, "max-result-bytes", mcpkit.MaxBytes,
		"split results larger than this many bytes of JSON into pages; 0 never splits")
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagIndex:          savedFiles(),
		flagScan:           savedFiles(),
		flagAudit:          savedFiles(),
		flagDrift:          savedFiles(),
		"max-result-bytes": complete.NoFiles,
	})
	return cmd
}

// ServeMCP runs `tfcensus mcp args...` but hands the server to serve instead of serving stdio,
// so tests can connect an in-process client.
func ServeMCP(
	ctx context.Context,
	d *app.Deps,
	args []string,
	serve func(context.Context, *mcpkit.Server) error,
) error {
	root, g := newRoot(d)
	g.mcpServe = serve
	root.SetArgs(append([]string{"mcp"}, args...))
	err := root.ExecuteContext(ctx)
	g.close()
	if err != nil {
		return fmt.Errorf("tfcensus mcp: %w", err)
	}
	return nil
}

func runMCP(cmd *cobra.Command, d *app.Deps, g *globals, f mcpFlags, serve serveFunc) error {
	maxBytes := f.maxBytes
	switch {
	case maxBytes < 0:
		return usageError(fmt.Errorf("--max-result-bytes must be 0 or more, got %d", maxBytes))
	case maxBytes == 0:
		maxBytes = -1 // mcpkit's value for no cap
	}
	cfg, err := g.loadConfig(cmd, d)
	if err != nil {
		return err
	}
	s := &saved{d: d, cfg: &cfg.Config, index: f.index, scan: f.scan, audit: f.audit, drift: f.drift}
	srv := mcpserver.New(mcpserver.Options{
		Version: buildinfo.Get().Version, Logger: d.Logger, Clock: d.Clock, MaxBytes: maxBytes, Backend: s.backend(),
	})
	d.Logger.InfoContext(cmd.Context(), "serving MCP on stdio", "files", s.sources())
	return serve(cmd.Context(), srv)
}

// backend answers the MCP tools through the same saved-report functions as tfcensus report.
func (s *saved) backend() mcpserver.Backend {
	return mcpserver.Backend{
		Units: func(_ context.Context, f report.UnitsFilter) (*report.Units, []render.Error, error) {
			return typed[*report.Units](s.units(f))
		},
		Inventory: func(_ context.Context, f report.InventoryFilter) (*report.Inventory, []render.Error, error) {
			return typed[*report.Inventory](s.inventory(f))
		},
		Whois: func(_ context.Context, queries, types []string) (any, []render.Error, error) {
			return typed[any](s.whois(queries, types))
		},
		Scan: func(_ context.Context, f report.ScanFilter) (*report.ScanSummary, []render.Error, error) {
			return typed[*report.ScanSummary](s.scanSummary(f))
		},
		Audit: func(_ context.Context, f report.AuditFilter) (*report.AuditFindings, []render.Error, error) {
			return typed[*report.AuditFindings](s.auditFindings(f))
		},
		Drift: func(_ context.Context, f report.DriftFilter) (*report.DriftSummary, []render.Error, error) {
			return typed[*report.DriftSummary](s.driftSummary(f))
		},
	}
}

func typed[T any](res *result, err error) (T, []render.Error, error) {
	var zero T
	if err != nil {
		return zero, nil, err
	}
	v, ok := res.data.(T)
	if !ok {
		return zero, nil, fmt.Errorf("saved result is %T", res.data) //nolint:err113 // a programming error
	}
	return v, res.errs, nil
}
