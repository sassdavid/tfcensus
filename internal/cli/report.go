package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/htmlreport"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/report"
)

const (
	colWorkspace = "WORKSPACE"
	colCategory  = "CATEGORY"
	colType      = "TYPE"

	flagOutput            = "output"
	flagScan              = "scan"
	flagAudit             = "audit"
	flagDrift             = "drift"
	flagPath              = "path"
	flagStatus            = "status"
	flagUnit              = "unit"
	flagAccount           = "account"
	flagCategory          = "category"
	flagTop               = "top"
	flagSeverity          = "severity"
	flagIncludeSuppressed = "include-suppressed"
)

func newReportCmd(d *app.Deps, g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Render saved scan, drift, audit and index files without collecting again",
		Long: `report reads JSON files that earlier runs wrote and renders them, offline: no AWS call, no
terragrunt and no state read. report html builds one static HTML page. The other subcommands
print the same summaries as the tools of tfcensus mcp, with the same filters.`,
		Args: noArgs,
	}
	cmd.AddCommand(
		newReportHTMLCmd(d, g),
		newReportUnitsCmd(d, g),
		newReportInventoryCmd(d, g),
		newReportScanCmd(d, g),
		newReportAuditCmd(d, g),
		newReportDriftCmd(d, g),
	)
	return cmd
}

func newReportHTMLCmd(d *app.Deps, _ *globals) *cobra.Command {
	var from []string
	var out string
	cmd := &cobra.Command{
		Use:   "html",
		Short: "Write a static HTML report from scan, drift and audit JSON",
		Long: `html writes one HTML file from the JSON that scan, drift and audit print with --format json.
The CSS and JavaScript are inside the file and a Content-Security-Policy blocks every network
request, so it opens offline and can be attached to a CI run.

The newest scan fills the scan section, with filters per account, category and type. With
two or more scan files, every scan becomes a point of a coverage trend. The newest drift and
audit files fill their sections. Drift shows attribute paths only, never values, even when
the drift JSON was written with --show-attributes.

The same input files always give the same bytes.`,
		Example: `  tfcensus report html --from scan.json -o census.html
  tfcensus report html --from scan-2026-11-01.json --from scan.json --from drift.json --from audit.json -o census.html`,
		Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if len(from) == 0 {
				return usageError(fmt.Errorf("pass at least one --%s file", flagFrom))
			}
			inputs, err := report.ReadAll(from)
			if err != nil {
				return usageError(err)
			}
			for _, in := range inputs {
				if in.Kind == report.KindIndex {
					return usageError(
						fmt.Errorf("%s is a state index; report html reads scan, drift and audit JSON", in.Path),
					)
				}
			}
			var buf bytes.Buffer
			if err = htmlreport.Render(&buf, inputs, buildinfo.Get().Version); err != nil {
				if errors.Is(err, htmlreport.ErrNoReports) {
					return usageError(err)
				}
				return err
			}
			return writeOutput(d, out, buf.Bytes())
		},
	}
	cmd.Flags().StringArrayVar(&from, flagFrom, nil, "scan, drift or audit JSON file (repeatable; .gz and .zst work)")
	cmd.Flags().StringVarP(&out, flagOutput, "o", "-", "file to write, or - for stdout")
	return cmd
}

// writeOutput writes data to stdout for "-", else to path through a temporary file in the same
// directory, so a reader never sees half a file.
func writeOutput(d *app.Deps, path string, data []byte) error {
	if path == "-" || path == "" {
		if _, err := d.Stdout.Write(data); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		return nil
	}
	path = filepath.Clean(path)
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err = errors.Join(werr, cerr); err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// writeSaved prints a saved-report result as the envelope MCP returns, or as a table.
func writeSaved(d *app.Deps, format render.Format, res *result, table func(io.Writer) error) error {
	if format == render.FormatJSON {
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), res.data)
		if res.errs != nil {
			env.Errors = res.errs
		}
		return render.JSON(d.Stdout, env)
	}
	return table(d.Stdout)
}

func checkTextOrJSON(g *globals) error {
	if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
		return usageError(err)
	}
	return nil
}

func newReportUnitsCmd(d *app.Deps, g *globals) *cobra.Command {
	var f report.UnitsFilter
	var index string
	cmd := &cobra.Command{
		Use:   "units",
		Short: "List the units of a saved state index",
		Long: `units lists the states in an index written by tfcensus inventory --save-index, with the
accounts and regions of their resources. It is what the units tool of tfcensus mcp returns.`,
		Example: `  tfcensus report units --index index.json.zst --path 'acme-live/prod/*'`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkTextOrJSON(g); err != nil {
				return err
			}
			cfg, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			s := &saved{d: d, cfg: &cfg.Config, index: index}
			res, err := s.units(f)
			if err != nil {
				return err
			}
			u, _ := res.data.(*report.Units)
			return writeSaved(d, g.outFormat, res, func(w io.Writer) error {
				rows := [][]string{{colUnit, colWorkspace, colStatus, "INSTANCES", "ACCOUNTS", "REGIONS"}}
				for _, r := range u.Units {
					rows = append(rows, []string{
						r.Unit, r.Workspace, string(r.Status), strconv.Itoa(r.Instances),
						dash(strings.Join(r.Accounts, ",")), dash(strings.Join(r.Regions, ",")),
					})
				}
				if werr := writeRows(w, rows); werr != nil {
					return werr
				}
				_, werr := fmt.Fprintf(w, "\n%d of %d units\n", len(u.Units), u.Total)
				return werr //nolint:wrapcheck // a write to stdout
			})
		},
	}
	cmd.Flags().StringVar(&index, flagIndex, "", "state index from tfcensus inventory --save-index")
	cmd.Flags().StringVar(&f.Path, flagPath, "", "unit path glob, * matches / too")
	cmd.Flags().StringVar(&f.Status, flagStatus, "", "state status: ok, absent, encrypted, unsupported or error")
	return cmd
}

func newReportInventoryCmd(d *app.Deps, g *globals) *cobra.Command {
	var f report.InventoryFilter
	var index string
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Count the managed resources of a saved state index per unit and type",
		Long: `inventory counts the managed resource instances in an index written by tfcensus inventory
--save-index, per unit and TF type, with the instances another state also manages. It is
what the inventory tool of tfcensus mcp returns.`,
		Example: `  tfcensus report inventory --index index.json.zst --account 111111111111 --type 'aws_iam_*'`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkTextOrJSON(g); err != nil {
				return err
			}
			cfg, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			res, err := (&saved{d: d, cfg: &cfg.Config, index: index}).inventory(f)
			if err != nil {
				return err
			}
			inv, _ := res.data.(*report.Inventory)
			return writeSaved(d, g.outFormat, res, func(w io.Writer) error {
				rows := [][]string{{colUnit, colWorkspace, "INSTANCES", "DOUBLE", "TYPES"}}
				for _, r := range inv.Units {
					rows = append(rows, []string{
						r.Unit, r.Workspace, strconv.Itoa(r.Instances),
						strconv.Itoa(r.DoubleManaged), strconv.Itoa(len(r.Types)),
					})
				}
				if werr := writeRows(w, rows); werr != nil {
					return werr
				}
				_, werr := fmt.Fprintf(w, "\n%d instances, %d double-managed\n", inv.Instances, inv.DoubleManaged)
				return werr //nolint:wrapcheck // a write to stdout
			})
		},
	}
	cmd.Flags().StringVar(&index, flagIndex, "", "state index from tfcensus inventory --save-index")
	cmd.Flags().StringVar(&f.Unit, flagUnit, "", "unit path glob, * matches / too")
	cmd.Flags().StringVar(&f.Account, flagAccount, "", "AWS account ID")
	cmd.Flags().StringVar(&f.Region, flagRegion, "", "AWS region")
	cmd.Flags().StringVar(&f.Type, flagType, "", "TF resource type glob, such as aws_iam_*")
	return cmd
}

func newReportScanCmd(d *app.Deps, g *globals) *cobra.Command {
	var f report.ScanFilter
	var from string
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Summarize a saved scan: counts and the top unmanaged resources",
		Long: `scan prints the per-account counts of a scan and its top findings, double-managed first,
then unmanaged. --category lists another category instead.

With --from it reads a file from tfcensus scan --format json. Without it, it reads the newest
snapshot in the scan cache and applies the current ownership rules to it, with no AWS call.
A snapshot older than cache.ttl is used anyway, with a warning. It is what the scan_summary
tool of tfcensus mcp returns.`,
		Example: `  tfcensus report scan --from scan.json --account 111111111111 --top 20
  tfcensus report scan --category undetermined`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkTextOrJSON(g); err != nil {
				return err
			}
			cfg, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			res, err := (&saved{d: d, cfg: &cfg.Config, scan: from}).scanSummary(f)
			if err != nil {
				return err
			}
			sum, _ := res.data.(*report.ScanSummary)
			return writeSaved(d, g.outFormat, res, func(w io.Writer) error { return writeScanSummaryText(w, sum) })
		},
	}
	cmd.Flags().StringVar(&from, flagFrom, "", "scan JSON (default: the newest scan cache snapshot)")
	cmd.Flags().StringVar(&f.Account, flagAccount, "", "AWS account ID")
	cmd.Flags().StringVar(&f.Region, flagRegion, "", "AWS region, or global")
	cmd.Flags().StringVar(&f.Category, flagCategory, "", "list this category (default: double-managed and unmanaged)")
	cmd.Flags().StringVar(&f.Type, flagType, "", "CloudFormation or layer type glob, such as AWS::EC2::*")
	cmd.Flags().IntVar(&f.Top, flagTop, report.DefaultTop, fmt.Sprintf("findings to list, 1 to %d", report.MaxTop))
	return cmd
}

func writeScanSummaryText(w io.Writer, s *report.ScanSummary) error {
	rows := [][]string{summaryColumns()}
	for i := range s.Accounts {
		rows = append(rows, summaryRow(&s.Accounts[i]))
	}
	if err := writeRows(w, rows); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\n%d matching findings by type:\n", s.Matching); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	rows = [][]string{{colType, "COUNT"}}
	for _, tc := range s.Types {
		rows = append(rows, []string{tc.Type, strconv.Itoa(tc.Count)})
	}
	if err := writeRows(w, rows); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\ntop %d:\n", len(s.Top)); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	rows = [][]string{{colCategory, colAccount, colRegion, colType, "ARN OR ID", "RULE"}}
	for i := range s.Top {
		f := &s.Top[i]
		rows = append(rows, []string{string(f.Category), f.Account, dash(f.Region), f.Type(), f.Key(), f.Rule})
	}
	if err := writeRows(w, rows); err != nil {
		return err
	}
	for _, warn := range s.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warn); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}
	return nil
}

func newReportAuditCmd(d *app.Deps, g *globals) *cobra.Command {
	var f report.AuditFilter
	var from string
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "List the findings of a saved backend audit, highest severity first",
		Long: `audit lists the findings in a file from tfcensus audit --format json, highest severity
first. It is what the audit_findings tool of tfcensus mcp returns.`,
		Example: `  tfcensus report audit --from audit.json --severity medium`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkTextOrJSON(g); err != nil {
				return err
			}
			cfg, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			res, err := (&saved{d: d, cfg: &cfg.Config, audit: from}).auditFindings(f)
			if err != nil {
				return err
			}
			af, _ := res.data.(*report.AuditFindings)
			return writeSaved(d, g.outFormat, res, func(w io.Writer) error {
				rows := [][]string{{"SEVERITY", "CHECK", "TARGET", "REASON"}}
				for _, fd := range af.Findings {
					rows = append(rows, []string{string(fd.Severity), fd.Rule, fd.Target, fd.Reason})
				}
				return writeRows(w, rows)
			})
		},
	}
	cmd.Flags().StringVar(&from, flagFrom, "", "audit JSON from tfcensus audit --format json")
	cmd.Flags().StringVar(&f.Rule, flagRule, "", "check ID glob, such as S3-*")
	cmd.Flags().StringVar(&f.Severity, flagSeverity, "", "lowest severity: info, low, medium or high")
	cmd.Flags().StringVar(&f.Account, flagAccount, "", "AWS account ID")
	cmd.Flags().BoolVar(&f.IncludeSuppressed, flagIncludeSuppressed, false, "list suppressed findings too")
	return cmd
}

func newReportDriftCmd(d *app.Deps, g *globals) *cobra.Command {
	var f report.DriftFilter
	var from string
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Summarize a saved drift run: unit statuses and drifted resources",
		Long: `drift lists the drifted and failed units of a file from tfcensus drift --format json, and
their drifted resources with attribute paths, never values. It runs no plan. It is what the
drift_summary tool of tfcensus mcp returns.`,
		Example: `  tfcensus report drift --from drift.json --unit 'acme-live/prod/*'`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkTextOrJSON(g); err != nil {
				return err
			}
			cfg, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			res, err := (&saved{d: d, cfg: &cfg.Config, drift: from}).driftSummary(f)
			if err != nil {
				return err
			}
			ds, _ := res.data.(*report.DriftSummary)
			return writeSaved(d, g.outFormat, res, func(w io.Writer) error {
				rows := [][]string{{colUnit, "ADDRESS", colType, "ACTION", "ATTRIBUTES"}}
				for _, c := range ds.Changes {
					rows = append(
						rows,
						[]string{c.Unit, c.Address, c.Type, string(c.Action), dash(strings.Join(c.Attributes, ","))},
					)
				}
				return writeRows(w, rows)
			})
		},
	}
	cmd.Flags().StringVar(&from, flagFrom, "", "drift JSON from tfcensus drift --format json")
	cmd.Flags().StringVar(&f.Unit, flagUnit, "", "unit path glob, * matches / too")
	cmd.Flags().
		StringVar(&f.Status, flagStatus, "", "unit status: drifted, failed, clean, early-exit or excluded (default drifted and failed)")
	return cmd
}
