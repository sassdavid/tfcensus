package cli

import (
	"cmp"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const (
	flagSaveIndex   = "save-index"
	flagConcurrency = "concurrency"
)

func newInventoryCmd(d *app.Deps, g *globals) *cobra.Command {
	var (
		filters     []string
		saveIndex   string
		concurrency int
	)
	cmd := &cobra.Command{
		Use:   "inventory [paths...]",
		Short: "Read each unit's state: versions, providers, resource counts, serial and lineage",
		Long: `inventory discovers units the way units does, then reads each unit's state from its s3
backend with s3:GetObject. It takes no lock, so it is safe to run during an apply. Other
workspaces of a unit are found with s3:ListBucket under workspace_key_prefix (default env:) and
listed as their own rows.

inventory prints identifiers and counts only: never attribute values. Raw state records which
providers a unit uses but not their versions, so provider versions come from the unit's
.terraform.lock.hcl when the repo has one.

State is read with the backend's credentials: the backend's profile when it names one, else the
profile that owns the bucket's account (accounts.map, then the --profile and --org accounts, then
the default credentials), with the backend's role_arn assumed from it. The bucket's account is
the role's account, or the unit's target account when there is no role.

A state that can't be read (access denied, a parse error) is reported for its unit and the run
continues; inventory then exits 1. An OpenTofu state with client-side encryption shows only its
serial and lineage. --save-index writes the identifier index that whois and scan read, compressed
with zstd for a .zst name and gzip for .gz.`,
		Example: `  tfcensus inventory ~/src/infra/acme-live
  tfcensus inventory --profile 'acme-*' --format json > inventory.json
  tfcensus inventory --format csv --save-index index.json.zst`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.outFormat.Check(
				render.FormatTTY, render.FormatPlain, render.FormatMD, render.FormatJSON, render.FormatCSV,
			); err != nil {
				return usageError(err)
			}
			if concurrency < 1 {
				return usageError(fmt.Errorf("--%s must be at least 1, got %d", flagConcurrency, concurrency))
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			return runInventory(cmd, d, g.outFormat, &res.Config, inventoryArgs{
				filters: filters, paths: args, saveIndex: saveIndex, concurrency: concurrency,
			})
		},
	}
	addDiscoveryFlags(cmd, &filters)
	addAccountFlags(cmd)
	f := cmd.Flags()
	f.StringVar(&saveIndex, flagSaveIndex, "",
		"write the identifier index to this file (.zst: zstd, .gz: gzip, else plain JSON)")
	f.IntVar(&concurrency, flagConcurrency, tfstate.DefaultConcurrency, "states read at the same time")
	return cmd
}

type inventoryArgs struct {
	filters, paths []string
	saveIndex      string
	concurrency    int
}

type inventoryData struct {
	Units         []tfstate.UnitState     `json:"units"`
	DoubleManaged []tfstate.DoubleManaged `json:"double_managed"`
}

func runInventory(cmd *cobra.Command, d *app.Deps, format render.Format, cfg *config.Config, a inventoryArgs) error {
	ctx := cmd.Context()
	loader := newLoader(d)
	disc, err := discoverUnits(cmd, d, cfg, a.filters, a.paths, loader)
	if err != nil {
		return err
	}
	var accts *accounts.Result
	if len(cfg.Accounts.Profiles) > 0 || cfg.Accounts.Org.Enabled {
		if accts, err = resolveAccounts(cmd, d, loader, cfg); err != nil {
			return err
		}
	}
	cwd, err := d.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	reader := tfstate.NewReader(tfstate.Options{
		Client:      stateClient(newBackends(loader, accts, cfg, d)),
		WorkDir:     cwd,
		Concurrency: a.concurrency,
		TFPath:      cfg.Binaries.Terraform,
		Logger:      d.Logger,
	})
	states := reader.Read(ctx, disc.Units)
	if states == nil {
		states = []tfstate.UnitState{}
	}
	index := tfstate.BuildIndex(states)
	data := inventoryData{Units: states, DoubleManaged: index.DoubleManaged}
	if err = writeInventory(d, format, data, disc); err != nil {
		return err
	}
	if a.saveIndex != "" {
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), index)
		if err = tfstate.WriteIndexFile(a.saveIndex, env); err != nil {
			return err
		}
		d.Logger.InfoContext(ctx, "wrote index", "path", a.saveIndex, "entries", len(index.Entries))
	}
	return errors.Join(discoveryError(disc), inventoryError(states))
}

// inventoryError is the error for units whose state couldn't be read: exit 4 when every read
// failed on credentials, else exit 1.
func inventoryError(states []tfstate.UnitState) error {
	failed, auth, read := 0, 0, 0
	for i := range states {
		switch states[i].Status {
		case tfstate.StatusSkipped:
			continue
		case tfstate.StatusError:
			failed++
			if errors.Is(states[i].Err, accounts.ErrAuth) {
				auth++
			}
		case tfstate.StatusOK, tfstate.StatusAbsent, tfstate.StatusEncrypted, tfstate.StatusUnsupported:
		}
		read++
	}
	if failed == 0 {
		return nil
	}
	err := fmt.Errorf("read state: %d of %d failed (see the notes above)", failed, read)
	if auth == read {
		return &exitError{code: ExitAuth, err: err}
	}
	return err
}

// newBackends returns the backend credentials for cfg's accounts.map and the resolved accounts.
func newBackends(l *accounts.Loader, accts *accounts.Result, cfg *config.Config, d *app.Deps) *accounts.Backends {
	mapping := map[string]string{}
	for id, ref := range cfg.Accounts.Map {
		if ref.Profile != "" {
			mapping[id] = ref.Profile
		}
	}
	return accounts.NewBackends(l, accts, mapping, d.Env.Lookup)
}

// backendRef names the credentials of u's backend.
func backendRef(u *discovery.Unit) accounts.BackendRef {
	be := u.Backend
	return accounts.BackendRef{
		Profile: be.Profile, RoleARN: be.RoleARN, Account: u.Target.Account,
		Region: cmp.Or(be.Region, u.Target.Region),
	}
}

// stateClient returns the S3 client that reads u's state.
func stateClient(b *accounts.Backends) func(context.Context, *discovery.Unit) (tfstate.Client, error) {
	return func(ctx context.Context, u *discovery.Unit) (tfstate.Client, error) {
		acct, region, err := b.Credentials(ctx, backendRef(u))
		if err != nil {
			return nil, err
		}
		return b.S3(acct, region), nil
	}
}

func writeInventory(d *app.Deps, format render.Format, data inventoryData, disc discovery.Result) error {
	switch format {
	case render.FormatJSON:
		env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), data)
		for _, e := range disc.Errors {
			env.Errors = append(env.Errors, render.Error{Target: e.Repo, Message: e.Err.Error()})
		}
		for i := range data.Units {
			if u := &data.Units[i]; u.Error != "" {
				env.Errors = append(env.Errors, render.Error{Target: stateLabel(u), Message: u.Error})
			}
		}
		return render.JSON(d.Stdout, env)
	case render.FormatCSV:
		return writeInventoryCSV(d.Stdout, data.Units)
	case render.FormatMD:
		return writeInventoryMarkdown(d.Stdout, data)
	case render.FormatAuto, render.FormatTTY, render.FormatPlain, render.FormatSARIF, render.FormatGitHub:
	}
	return writeInventoryTable(d.Stdout, data)
}

// stateLabel is repo/path, with :workspace for a workspace other than default.
func stateLabel(u *tfstate.UnitState) string {
	if u.Workspace == tfstate.DefaultWorkspace {
		return u.Unit()
	}
	return u.Unit() + ":" + u.Workspace
}

func inventoryColumns() []string {
	return []string{
		"REPO",
		colUnit,
		"WORKSPACE",
		colStatus,
		"SERIAL",
		"VERSION",
		"RESOURCES",
		"SIZE",
		"MODIFIED",
		"PROVIDERS",
	}
}

func inventoryRow(u *tfstate.UnitState) []string {
	serial := "-"
	if u.Serial != nil {
		serial = strconv.FormatUint(*u.Serial, 10)
	}
	version := dash(u.TerraformVersion)
	if u.Engine != "" && u.TerraformVersion != "" {
		version += " (" + u.Engine + ")"
	}
	resources := "-"
	if u.Status == tfstate.StatusOK {
		resources = strconv.Itoa(u.Instances)
	}
	modified := "-"
	if u.LastModified != nil {
		modified = u.LastModified.UTC().Format("2006-01-02 15:04")
	}
	return []string{
		u.Repo, u.Path, u.Workspace, string(u.Status), serial, version, resources,
		humanSize(u.Size), modified, providersCell(u.Providers),
	}
}

func providersCell(ps []tfstate.ProviderVersion) string {
	if len(ps) == 0 {
		return "-"
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.Source
		if p.Version != "" {
			parts[i] += " " + p.Version
		}
	}
	return strings.Join(parts, ", ")
}

func humanSize(n int64) string {
	const unit = 1024
	switch {
	case n <= 0:
		return "-"
	case n < unit:
		return strconv.FormatInt(n, 10) + " B"
	case n < unit*unit:
		return fmt.Sprintf("%.1f KiB", float64(n)/unit)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(unit*unit))
	}
}

// inventoryNotes lists errors, warnings and double-managed resources, one line each.
func inventoryNotes(data inventoryData) []string {
	var notes []string
	for i := range data.Units {
		u := &data.Units[i]
		if u.Error != "" {
			notes = append(notes, stateLabel(u)+": error: "+u.Error)
		}
		for _, w := range u.Warnings {
			notes = append(notes, stateLabel(u)+": "+w)
		}
	}
	for _, dm := range data.DoubleManaged {
		refs := make([]string, len(dm.Refs))
		for i, r := range dm.Refs {
			label := r.Unit
			if r.Workspace != tfstate.DefaultWorkspace {
				label += ":" + r.Workspace
			}
			refs[i] = label + " (" + r.Address + ")"
		}
		notes = append(notes, fmt.Sprintf("double-managed %s %s by %s (matched on %s)",
			dm.Type, dm.Key, strings.Join(refs, " and "), dm.Kind))
	}
	return notes
}

func inventorySummary(data inventoryData) string {
	counts := make(map[tfstate.Status]int)
	instances := 0
	for i := range data.Units {
		counts[data.Units[i].Status]++
		instances += data.Units[i].Instances
	}
	var parts []string
	for _, s := range []tfstate.Status{
		tfstate.StatusOK, tfstate.StatusAbsent, tfstate.StatusEncrypted, tfstate.StatusUnsupported,
		tfstate.StatusError, tfstate.StatusSkipped,
	} {
		parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
	}
	return fmt.Sprintf("%d states: %s; %d resource instances, %d double-managed",
		len(data.Units), strings.Join(parts, ", "), instances, len(data.DoubleManaged))
}

func writeInventoryTable(w io.Writer, data inventoryData) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
	_, _ = fmt.Fprintln(tw, strings.Join(inventoryColumns(), "\t"))
	for i := range data.Units {
		_, _ = fmt.Fprintln(tw, strings.Join(inventoryRow(&data.Units[i]), "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}
	if notes := inventoryNotes(data); len(notes) > 0 {
		_, _ = fmt.Fprintln(w)
		for _, n := range notes {
			_, _ = fmt.Fprintln(w, n)
		}
	}
	_, _ = fmt.Fprintln(w)
	if _, err := fmt.Fprintln(w, inventorySummary(data)); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}
	return nil
}

func writeInventoryMarkdown(w io.Writer, data inventoryData) error {
	var b strings.Builder
	cols := inventoryColumns()
	b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	b.WriteString(strings.Repeat("| --- ", len(cols)) + "|\n")
	for i := range data.Units {
		row := inventoryRow(&data.Units[i])
		for j, cell := range row {
			row[j] = strings.ReplaceAll(cell, "|", `\|`)
		}
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	if notes := inventoryNotes(data); len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
	}
	b.WriteString("\n" + inventorySummary(data) + "\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}
	return nil
}

func writeInventoryCSV(w io.Writer, units []tfstate.UnitState) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"repo", "path", "workspace", "bucket", "key", "region", "status", "engine", "state_version",
		"terraform_version", "serial", "lineage", "encrypted", "size", "last_modified", "resources",
		"instances", "data_sources", "providers", "resource_types", "error",
	})
	for i := range units {
		u := &units[i]
		serial, lastModified, stateVersion := "", "", ""
		if u.Serial != nil {
			serial = strconv.FormatUint(*u.Serial, 10)
		}
		if u.LastModified != nil {
			lastModified = u.LastModified.UTC().Format(time.RFC3339)
		}
		if u.StateVersion != 0 {
			stateVersion = strconv.Itoa(u.StateVersion)
		}
		providers := make([]string, len(u.Providers))
		for j, p := range u.Providers {
			providers[j] = p.Source
			if p.Version != "" {
				providers[j] += "@" + p.Version
			}
		}
		types := make([]string, 0, len(u.ResourceTypes))
		for _, t := range slices.Sorted(maps.Keys(u.ResourceTypes)) {
			types = append(types, t+"="+strconv.Itoa(u.ResourceTypes[t]))
		}
		_ = cw.Write([]string{
			u.Repo, u.Path, u.Workspace, u.Bucket, u.Key, u.Region, string(u.Status), u.Engine, stateVersion,
			u.TerraformVersion, serial, u.Lineage, strconv.FormatBool(u.Encrypted), strconv.FormatInt(u.Size, 10),
			lastModified, strconv.Itoa(u.Resources), strconv.Itoa(u.Instances), strconv.Itoa(u.DataSources),
			strings.Join(providers, ";"), strings.Join(types, ";"), u.Error,
		})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}
	return nil
}
