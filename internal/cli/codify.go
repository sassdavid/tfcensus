package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/codify"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	flagFrom     = "from"
	flagInto     = "into"
	flagSelect   = "select"
	flagTarget   = "target"
	flagImportBy = "import-by"
	flagQuery    = "query"
	flagForce    = "force"
)

type codifyArgs struct {
	from, into string
	types      []string
	selectExpr string
	target     string
	importBy   string
	query      bool
	force      bool
}

func newCodifyCmd(d *app.Deps, g *globals) *cobra.Command {
	a := codifyArgs{}
	cmd := &cobra.Command{
		Use:   "codify --from scan.json --into <dir>",
		Short: "Write import blocks for unmanaged resources from a scan report",
		Long: `codify reads the unmanaged findings of a scan report (tfcensus scan --format json) and writes
import blocks into --into as imports_tfcensus_<YYYYMMDD>.tf, dated by the UTC clock. The blocks are
a starting point for adoption: plan with -generate-config-out, review the generated config, and
move it into your code. codify never runs plan or apply and writes nothing outside --into.

Each block imports by identity when the target can: terraform or OpenTofu 1.12 or later, an aws
provider of 6.0 or later in the directory's .terraform.lock.hcl (or no lock file), and a type
whose identity attributes the scan gives. Otherwise it imports by id, from the typemap's id
template. A finding with neither is listed as skipped with the reason.

The target engine comes from the lock file's registry (registry.opentofu.org means OpenTofu),
else from the --tf-path binary's name; its version from '<tf-path> version -json'. --target
terraform@1.12.2 or opentofu@1.11.0 skips the detection. When detection fails, codify writes id
imports.

Names come from the Name tag, then the resource's name, then the last part of its ID, as
lowercase letters, digits and underscores. A name that is taken, in this run or by a resource or
import block already in --into, gets _2, _3 and so on. A resource that an import block in --into
already imports is skipped.

--query also writes query_tfcensus_<YYYYMMDD>.tfquery.hcl with one list block per type and
region, for types that have a provider list resource without required list config. terraform
query -generate-config-out turns it into config (terraform 1.14 or later; OpenTofu has no query).

--select takes a CEL expression over the same resource variable as ownership rules.

codify refuses to overwrite a file unless --force is set. The exit code is 2 for bad flags or
input and 1 when a file can't be written.`,
		Example: `  tfcensus scan --format json > scan.json
  tfcensus codify --from scan.json --into live/prod/eu-west-1/network --types 'AWS::EC2::*'
  tfcensus codify --from scan.json --into live/prod/iam --select 'resource.tags["team"] == "platform"'
  tfcensus codify --from scan.json --into iam --target terraform@1.14.0 --query`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
				return usageError(err)
			}
			if a.importBy != "auto" && a.importBy != codify.ByID {
				return usageError(fmt.Errorf("--%s %q: want auto or id", flagImportBy, a.importBy))
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			return runCodify(cmd, d, g.outFormat, &res.Config, a)
		},
	}
	f := cmd.Flags()
	f.StringVar(&a.from, flagFrom, "", "scan report from tfcensus scan --format json, or - for stdin")
	f.StringVar(&a.into, flagInto, "", "directory of the unit to write the files into; it must exist")
	f.StringSliceVar(&a.types, flagTypes, nil,
		"resource types to codify, as CloudFormation or TF type globs such as 'AWS::IAM::*' or 'aws_iam_role'")
	f.StringVar(&a.selectExpr, flagSelect, "", "CEL expression over resource that picks the findings to codify")
	f.StringVar(&a.target, flagTarget, "", "engine and version the blocks are for, such as terraform@1.12.2 or "+
		"opentofu@1.11.0 (default: detect)")
	f.StringVar(&a.importBy, flagImportBy, "auto", "auto (identity when the target can) or id")
	f.BoolVar(&a.query, flagQuery, false, "also write a .tfquery.hcl file with list blocks for terraform query")
	f.BoolVar(&a.force, flagForce, false, "overwrite files codify wrote before")
	_ = cmd.MarkFlagRequired(flagFrom)
	_ = cmd.MarkFlagRequired(flagInto)
	return cmd
}

// codifyReport is the data of codify --format json.
type codifyReport struct {
	*codify.Result

	Into  string   `json:"into"`
	Files []string `json:"files"`
	Next  []string `json:"next"`
}

func runCodify(cmd *cobra.Command, d *app.Deps, format render.Format, cfg *config.Config, a codifyArgs) error {
	ctx := cmd.Context()
	o := codify.Options{Types: scan.TypeMatcher(a.types), Query: a.query, ImportByID: a.importBy == codify.ByID}
	var err error
	if o.Map, err = typemap.Load(); err != nil {
		return err
	}
	if a.selectExpr != "" {
		if o.Select, err = ownership.NewSelector(a.selectExpr); err != nil {
			return usageError(err)
		}
	}
	findings, err := readScanReport(d, a.from)
	if err != nil {
		return err
	}
	if fi, statErr := os.Stat(a.into); statErr != nil || !fi.IsDir() {
		return usageError(fmt.Errorf("--%s %s is not a directory", flagInto, a.into))
	}
	if a.target != "" {
		if o.Target, err = codify.ParseTarget(a.target); err != nil {
			return usageError(err)
		}
	} else {
		runner := execx.NewRunner(d.Logger, execx.WithEnviron(func() []string { return d.Env }))
		o.Target, err = codify.Detect(ctx, runner, cfg.Binaries.Terraform, a.into)
		if err != nil {
			d.Logger.WarnContext(ctx, "could not detect the target; writing id imports (set --target)", "error", err)
			o.Target.Version = nil
		}
	}
	// codify's own files from today are replaced, so they don't count as existing imports.
	if o.Existing, err = codify.ReadExisting(a.into, codify.OutputNames(d.Clock())...); err != nil {
		return usageError(fmt.Errorf("read --%s: %w", flagInto, err))
	}
	o.From, o.Date = filepath.Base(a.from), d.Clock()
	if a.from == "-" {
		o.From = "stdin"
	}
	res, err := codify.Generate(findings, o)
	if err != nil {
		return usageError(err)
	}
	if err = codify.Write(a.into, res.Files, a.force); err != nil {
		if errors.Is(err, codify.ErrExists) {
			return usageError(err)
		}
		return err
	}
	_, tgErr := os.Stat(filepath.Join(a.into, "terragrunt.hcl"))
	rep := &codifyReport{
		Result: res, Into: a.into, Files: res.FileNames(),
		Next: codify.NextSteps(a.into, tgErr == nil, o.Target, res),
	}
	if format == render.FormatJSON {
		return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), rep))
	}
	return writeCodifyText(d.Stdout, rep)
}

// readScanReport returns the findings of a scan --format json report.
func readScanReport(d *app.Deps, path string) ([]scan.Finding, error) {
	var r io.Reader
	if path == "-" {
		r = d.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, usageError(fmt.Errorf("read --%s: %w", flagFrom, err))
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	var env struct {
		Tool string       `json:"tool"`
		Data *scan.Report `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&env); err != nil {
		return nil, usageError(fmt.Errorf("read --%s: %w", flagFrom, err))
	}
	if env.Tool != render.ToolName || env.Data == nil || env.Data.Findings == nil {
		return nil, usageError(fmt.Errorf("--%s %s is not a tfcensus scan report (scan --format json)", flagFrom, path))
	}
	return env.Data.Findings, nil
}

func writeCodifyText(w io.Writer, rep *codifyReport) error {
	var b strings.Builder
	switch {
	case len(rep.Imports) == 0:
		fmt.Fprintf(&b, "Nothing to import: %d unmanaged finding(s) selected.\n", rep.Selected)
	default:
		byIdentity := 0
		for i := range rep.Imports {
			if rep.Imports[i].By == codify.ByIdentity {
				byIdentity++
			}
		}
		fmt.Fprintf(&b, "Wrote %d import block(s) (%d by identity, %d by id) for %s (%s) into %s:\n",
			len(rep.Imports), byIdentity, len(rep.Imports)-byIdentity, rep.Target, rep.Target.Source, rep.Into)
		for _, f := range rep.Files {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	if len(rep.Skipped) > 0 {
		fmt.Fprintf(&b, "\nSkipped (%d)\n", len(rep.Skipped))
		for _, s := range rep.Skipped {
			fmt.Fprintf(&b, "  %s %s: %s\n", s.Type, s.Key, s.Reason)
		}
	}
	if len(rep.Notes) > 0 {
		b.WriteString("\n")
		for _, n := range rep.Notes {
			b.WriteString("note: " + n + "\n")
		}
	}
	if len(rep.Next) > 0 {
		b.WriteString("\nNext steps\n")
		for _, n := range rep.Next {
			b.WriteString("  " + n + "\n")
		}
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write codify: %w", err)
	}
	return nil
}
