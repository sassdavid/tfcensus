package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/mcpkit"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/report"
	"github.com/sassdavid/tfcensus/internal/scan"
)

// Name is the server_info name.
const Name = "tfcensus"

const instructions = `tfcensus answers questions about a Terraform/OpenTofu and Terragrunt fleet on AWS from ` +
	`reports that earlier tfcensus runs saved: which units exist, what they manage, which states manage ` +
	`an ARN or ID (whois), what is unmanaged (scan_summary), backend audit findings and drift. The tools ` +
	`never call AWS, run terraform or terragrunt, or write anything, so answers are only as fresh as the ` +
	`saved files; check source.created and the warnings. Every result is the JSON envelope of ` +
	`tfcensus report or whois --index with --format json. When the envelope has page.next_cursor, call ` +
	`the same tool with only that cursor to get the rest.`

// Tools lists the tool names, in registration order.
func Tools() []string {
	return []string{"units", "inventory", "whois", "scan_summary", "audit_findings", "drift_summary"}
}

// Backend answers the tools from saved files. Errors fail the call; the returned [render.Error]
// list says what a result misses.
type Backend struct {
	Units     func(ctx context.Context, f report.UnitsFilter) (*report.Units, []render.Error, error)
	Inventory func(ctx context.Context, f report.InventoryFilter) (*report.Inventory, []render.Error, error)
	Whois     func(ctx context.Context, queries, types []string) (any, []render.Error, error)
	Scan      func(ctx context.Context, f report.ScanFilter) (*report.ScanSummary, []render.Error, error)
	Audit     func(ctx context.Context, f report.AuditFilter) (*report.AuditFindings, []render.Error, error)
	Drift     func(ctx context.Context, f report.DriftFilter) (*report.DriftSummary, []render.Error, error)
}

// Options configures [New].
type Options struct {
	Version  string
	Logger   *slog.Logger
	Clock    func() time.Time
	MaxBytes int // see [mcpkit.Options]
	Backend  Backend
}

// New returns the MCP server with every tfcensus tool. None of them is destructive or reaches
// the network.
func New(o Options) *mcpkit.Server {
	s := mcpkit.NewServer(mcpkit.Options{
		Name:         Name,
		Version:      o.Version,
		Instructions: instructions,
		Logger:       o.Logger,
		Clock:        o.Clock,
		MaxBytes:     o.MaxBytes,
	})
	t := &tools{o: o}
	mcpkit.AddTool(s, mcpkit.Tool{
		Name:        "units",
		Title:       "Units",
		Description: "Lists the units and workspaces in the saved state index, with state status, instance count, accounts and regions.",
	}, t.units)
	mcpkit.AddTool(s, mcpkit.Tool{
		Name:        "inventory",
		Title:       "Inventory per unit",
		Description: "Counts the managed resource instances in the saved state index per unit and TF type, with double-managed counts.",
	}, t.inventory)
	mcpkit.AddTool(s, mcpkit.Tool{
		Name: "whois", Title: "Who manages a resource",
		Description: "Finds which units and addresses manage each ARN or TF resource ID in the saved state index, " +
			"or which ownership rule explains an unmanaged one.",
	}, t.whois)
	mcpkit.AddTool(s, mcpkit.Tool{
		Name:  "scan_summary",
		Title: "Scan summary",
		Description: "Gives per-account category counts and coverage of the last scan, counts per resource type, and its " +
			"top double-managed and unmanaged resources, from the saved scan JSON or the newest scan cache snapshot.",
	}, t.scan)
	mcpkit.AddTool(s, mcpkit.Tool{
		Name: "audit_findings", Title: "Backend audit findings",
		Description: "Lists the state backend audit findings of the saved audit JSON, highest severity first.",
	}, t.audit)
	mcpkit.AddTool(s, mcpkit.Tool{
		Name: "drift_summary", Title: "Drift summary",
		Description: "Lists drifted and failed units and their drifted resources with attribute paths from the saved " +
			"drift JSON; it runs no plan.",
	}, t.drift)
	return s
}

type tools struct{ o Options }

func (t *tools) envelope(data any, errs []render.Error) render.Envelope {
	env := render.NewEnvelope(t.o.Version, t.o.Clock(), data)
	if errs != nil {
		env.Errors = errs
	}
	return env
}

type unitsInput struct {
	mcpkit.Paging

	Path   string `json:"path,omitempty"   jsonschema:"Unit path glob; * also matches /, such as acme-live/prod/*."`
	Status string `json:"status,omitempty" jsonschema:"State status: ok, absent, encrypted, unsupported or error."`
}

func (t *tools) units(ctx context.Context, _ *mcp.CallToolRequest, in unitsInput) (*mcpkit.Result, error) {
	u, errs, err := t.o.Backend.Units(ctx, report.UnitsFilter{Path: in.Path, Status: in.Status})
	if err != nil {
		return nil, err
	}
	pager := mcpkit.SlicePager(u.Units, "units", nil, func(rows []report.UnitRow) any {
		return &report.Units{Total: u.Total, Units: rows}
	})
	return &mcpkit.Result{Envelope: t.envelope(u, errs), Pager: pager}, nil
}

type inventoryInput struct {
	mcpkit.Paging

	Unit    string `json:"unit,omitempty"    jsonschema:"Unit path glob; * also matches /."`
	Account string `json:"account,omitempty" jsonschema:"AWS account ID."`
	Region  string `json:"region,omitempty"  jsonschema:"AWS region, such as eu-west-1."`
	Type    string `json:"type,omitempty"    jsonschema:"TF resource type glob, such as aws_iam_*."`
}

func (t *tools) inventory(ctx context.Context, _ *mcp.CallToolRequest, in inventoryInput) (*mcpkit.Result, error) {
	inv, errs, err := t.o.Backend.Inventory(ctx, report.InventoryFilter{
		Unit: in.Unit, Account: in.Account, Region: in.Region, Type: in.Type,
	})
	if err != nil {
		return nil, err
	}
	rank := func(r report.InventoryRow) int { return -r.DoubleManaged }
	pager := mcpkit.SlicePager(inv.Units, "units", rank, func(rows []report.InventoryRow) any {
		return &report.Inventory{Instances: inv.Instances, DoubleManaged: inv.DoubleManaged, Units: rows}
	})
	return &mcpkit.Result{Envelope: t.envelope(inv, errs), Pager: pager}, nil
}

type whoisInput struct {
	Queries []string `json:"queries"         jsonschema:"ARNs or TF resource IDs, such as arn:aws:s3:::acme-assets or sg-0123456789abcdef0."`
	Types   []string `json:"types,omitempty" jsonschema:"TF resource types to look in, such as aws_security_group. Default: the type the ARN maps to, else every type."` //nolint:lll // struct tag
}

func (t *tools) whois(ctx context.Context, _ *mcp.CallToolRequest, in whoisInput) (*mcpkit.Result, error) {
	var queries []string
	for _, q := range in.Queries {
		if q = strings.TrimSpace(q); q != "" {
			queries = append(queries, q)
		}
	}
	if len(queries) == 0 {
		return nil, fmt.Errorf("%w %q", mcpkit.ErrArgument, "queries")
	}
	data, errs, err := t.o.Backend.Whois(ctx, queries, in.Types)
	if err != nil {
		return nil, err
	}
	return &mcpkit.Result{Envelope: t.envelope(data, errs)}, nil
}

type scanInput struct {
	mcpkit.Paging

	Account  string `json:"account,omitempty"  jsonschema:"AWS account ID."`
	Region   string `json:"region,omitempty"   jsonschema:"AWS region, or global. Counts are then per region."`
	Category string `json:"category,omitempty" jsonschema:"List this category instead of double-managed and unmanaged: managed, double-managed, unmanaged, other-iac, aws-managed, derived, ignored or undetermined."` //nolint:lll // struct tag
	Type     string `json:"type,omitempty"     jsonschema:"CloudFormation or layer type glob, such as AWS::EC2::*."`
	Top      int    `json:"top,omitempty"      jsonschema:"Findings to list, 1 to 500. Default 50."`
}

func (t *tools) scan(ctx context.Context, _ *mcp.CallToolRequest, in scanInput) (*mcpkit.Result, error) {
	sum, errs, err := t.o.Backend.Scan(ctx, report.ScanFilter{
		Account: in.Account, Region: in.Region, Category: in.Category, Type: in.Type, Top: in.Top,
	})
	if err != nil {
		return nil, err
	}
	rank := func(f scan.Finding) int { return report.CategoryRank(f.Category) }
	pager := mcpkit.SlicePager(sum.Top, "findings", rank, func(top []scan.Finding) any {
		cp := *sum
		cp.Top = top
		return &cp
	})
	return &mcpkit.Result{Envelope: t.envelope(sum, errs), Pager: pager}, nil
}

type auditInput struct {
	mcpkit.Paging

	Rule              string `json:"rule,omitempty"               jsonschema:"Check ID glob, such as S3-* or LK-STALE."`
	Severity          string `json:"severity,omitempty"           jsonschema:"Lowest severity to list: info, low, medium or high."`
	Account           string `json:"account,omitempty"            jsonschema:"AWS account ID."`
	IncludeSuppressed bool   `json:"include_suppressed,omitempty" jsonschema:"List suppressed findings too, with their suppression reason."`
}

func (t *tools) audit(ctx context.Context, _ *mcp.CallToolRequest, in auditInput) (*mcpkit.Result, error) {
	af, errs, err := t.o.Backend.Audit(ctx, report.AuditFilter{
		Rule: in.Rule, Severity: in.Severity, Account: in.Account, IncludeSuppressed: in.IncludeSuppressed,
	})
	if err != nil {
		return nil, err
	}
	rank := func(f audit.Finding) int { return report.SeverityRank(f.Severity) }
	pager := mcpkit.SlicePager(af.Findings, "findings", rank, func(fs []audit.Finding) any {
		cp := *af
		cp.Findings = fs
		return &cp
	})
	return &mcpkit.Result{Envelope: t.envelope(af, errs), Pager: pager}, nil
}

type driftInput struct {
	mcpkit.Paging

	Unit   string `json:"unit,omitempty"   jsonschema:"Unit path glob; * also matches /."`
	Status string `json:"status,omitempty" jsonschema:"Unit status: drifted, failed, clean, early-exit or excluded. Default: drifted and failed."`
}

func (t *tools) drift(ctx context.Context, _ *mcp.CallToolRequest, in driftInput) (*mcpkit.Result, error) {
	ds, errs, err := t.o.Backend.Drift(ctx, report.DriftFilter{Unit: in.Unit, Status: in.Status})
	if err != nil {
		return nil, err
	}
	pager := mcpkit.SlicePager(ds.Changes, "changes", nil, func(cs []report.DriftChange) any {
		cp := *ds
		cp.Changes = cs
		return &cp
	})
	return &mcpkit.Result{Envelope: t.envelope(ds, errs), Pager: pager}, nil
}
