package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	flagTypes           = "types"
	flagLayers          = "layers"
	flagRules           = "rules"
	flagMinCount        = "min-count"
	flagExplain         = "explain"
	flagExplainCoverage = "explain-coverage"
	flagRefresh         = "refresh"
	flagIndex           = "index"
)

type scanArgs struct {
	filters, paths  []string
	types           []string
	rules           []string
	failOn          []string
	minCount        int
	explain         string
	explainCoverage bool
	refresh         bool
	index           string
	concurrency     int
}

func newScanCmd(d *app.Deps, g *globals) *cobra.Command {
	a := scanArgs{}
	formats := []render.Format{
		render.FormatTTY,
		render.FormatPlain,
		render.FormatMD,
		render.FormatJSON,
		render.FormatCSV,
		render.FormatSARIF,
		render.FormatGitHub,
	}
	cmd := &cobra.Command{
		Use:   "scan [paths...]",
		Short: "Find AWS resources that no unit manages",
		Long: `scan lists the live resources of every selected account and region, matches them against
the state of the discovered units, and puts each resource in one category:

  managed         matched to one state resource
  double-managed  matched in more than one state
  unmanaged       no state manages it and no ownership rule matches
  other-iac       owned by CloudFormation, CDK, Pulumi or a Kubernetes controller
  aws-managed     created by AWS, such as the default VPC
  derived         a child of another resource, such as a NAT gateway's network interface
  ignored         a user rule hides it
  undetermined    its type has no mapping, so tfcensus can't tell

The flow is: capability probe, discovery, state index, inventory layers, matching, then the
ownership rules (the built-in packs, then rules from the config and --rules files, first match
wins). scan --explain <arn> prints how one resource was categorized; --explain-coverage prints
which layer listed each type in each account and region.

Coverage is (managed + double-managed) / (managed + double-managed + unmanaged) per account.
aws-managed, derived, other-iac, ignored and undetermined resources don't count.

scan caches the inventory and state index under $XDG_CACHE_HOME/tfcensus/scan for cache.ttl
(default 1h), keyed by the accounts, regions, layers, types and units. A re-run with other rules
makes no AWS calls. --refresh lists again.

The exit code is 3 when --fail-on finds at least --min-count findings, 1 when a layer, a state
read or discovery failed, 4 when every failure was a credential problem, and 0 otherwise.`,
		Example: `  tfcensus scan ~/src/infra/acme-live --profile acme-nonprod --region eu-west-1
  tfcensus scan --types 'AWS::IAM::*' --fail-on unmanaged
  tfcensus scan --rules extra-rules.yaml --format md > scan.md
  tfcensus scan --explain arn:aws:ec2:eu-west-1:111111111111:security-group/sg-0123456789abcdef0`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.checkFormats(formats...); err != nil {
				return usageError(err)
			}
			if a.minCount < 1 {
				return usageError(fmt.Errorf("--%s must be at least 1, got %d", flagMinCount, a.minCount))
			}
			if a.concurrency < 1 {
				return usageError(fmt.Errorf("--%s must be at least 1, got %d", flagConcurrency, a.concurrency))
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			a.paths = args
			return runScan(cmd, d, g.sink(cmd, d), &res.Config, a)
		},
	}
	addDiscoveryFlags(cmd, &a.filters)
	addOutputFlags(cmd, g, formats...)
	addAccountFlags(cmd, d)
	addRegionFlag(cmd)
	f := cmd.Flags()
	f.StringSliceVar(&a.types, flagTypes, nil,
		"resource types to scan, as CloudFormation or TF type globs such as 'AWS::IAM::*' or 'aws_security_group*'")
	f.String(flagLayers, inventory.LayersAuto, "inventory layers: auto, or a comma-separated list of any of "+
		complete.Join(layerChoices()[1:])+" (config scan.layers)")
	f.StringArrayVar(&a.rules, flagRules, nil, "YAML file with more ownership rules under rules: (repeatable)")
	f.StringSliceVar(
		&a.failOn,
		flagFailOn,
		nil,
		complete.Usage("exit 3 when findings are in these categories: {values}", findingGateChoices()),
	)
	f.IntVar(&a.minCount, flagMinCount, 1, "findings --fail-on needs before it exits 3")
	f.StringVar(&a.explain, flagExplain, "", "print how the resource with this ARN or ID was categorized")
	f.BoolVar(&a.explainCoverage, flagExplainCoverage, false,
		"print which layer listed each type in each account and region")
	f.BoolVar(&a.refresh, flagRefresh, false, "list and read states again even when the cache is fresh")
	f.StringVar(&a.index, flagIndex, "", "read the state index from this file (from inventory --save-index) "+
		"instead of reading states")
	f.IntVar(&a.concurrency, flagConcurrency, tfstate.DefaultConcurrency, "states read at the same time")
	cmd.MarkFlagsMutuallyExclusive(flagExplain, flagExplainCoverage)
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagTypes:       complete.DynamicList(complete.Timeout, complete.Types()),
		flagLayers:      complete.List(layerChoices()),
		flagRules:       complete.Files("yaml", "yml"),
		flagFailOn:      complete.List(findingGateChoices()),
		flagMinCount:    complete.NoFiles,
		flagExplain:     complete.NoFiles,
		flagIndex:       savedFiles(),
		flagConcurrency: complete.NoFiles,
	})
	return cmd
}

func layerChoices() []complete.Choice {
	return []complete.Choice{
		{Value: inventory.LayersAuto, Desc: "every layer the capability probe allows"},
		{Value: inventory.LayerConfig, Desc: "AWS Config"},
		{Value: "re", Desc: "Resource Explorer"},
		{Value: "cc", Desc: "Cloud Control, for allowlisted types"},
		{Value: inventory.LayersNative, Desc: "every native lister"},
	}
}

// findingGateChoices are the categories scan and whois --fail-on accept.
func findingGateChoices() []complete.Choice {
	return []complete.Choice{
		{Value: string(scan.CategoryUnmanaged), Desc: "no state manages it"},
		{Value: string(scan.CategoryDoubleManaged), Desc: "more than one state manages it"},
	}
}

// scanEnv is everything a scan run shares.
type scanEnv struct {
	cmd    *cobra.Command
	d      *app.Deps
	cfg    *config.Config
	a      scanArgs
	m      *typemap.Map
	types  scan.TypeMatcher
	cfn    []string // CloudFormation types --types selects; empty means every type
	layers []string
	cc     []string
	failOn []scan.Category
}

func runScan(cmd *cobra.Command, d *app.Deps, sink render.Sink, cfg *config.Config, a scanArgs) error {
	if (a.explain != "" || a.explainCoverage) && len(sink.Outputs) > 0 {
		return usageError(fmt.Errorf("--%s writes the scan report; it doesn't work with --explain or "+
			"--explain-coverage", flagOut))
	}
	format := sink.Format
	e, err := newScanEnv(cmd, d, cfg, a)
	if err != nil {
		return err
	}
	inState := &stateLookup{}
	eng, err := ownershipEngine(d, cfg, a.rules, inState.has)
	if err != nil {
		return err
	}
	loader := newLoader(d)
	disc, err := discoverUnits(cmd, d, cfg, a.filters, a.paths, loader)
	if err != nil {
		return err
	}
	snap, err := e.snapshot(loader, disc)
	if err != nil {
		return err
	}
	inState.index(snap.Index)
	results := match.New(e.m, snap.Index).Match(snap.Inventory.Resources)
	ix := ownership.NewIndex(snap.Inventory.Resources)
	if a.explain != "" {
		return e.writeExplain(format, results, eng, ix)
	}
	if a.explainCoverage {
		return writeCoverage(d, format, snap.Inventory.Coverage)
	}
	findings := scan.Classify(results, eng, e.m, ix)
	findings = slices.DeleteFunc(findings, func(f scan.Finding) bool {
		return !e.types.Match(f.CFNType, f.SourceType, f.TFTypes)
	})
	sortFindings(findings)
	rep := &scan.Report{
		Findings: findings, Runs: snap.Inventory.Runs, Coverage: snap.Inventory.Coverage,
		Warnings: []string{}, Cached: snap.cached,
	}
	rep.Regions, rep.Accounts = scan.Summarize(findings)
	for _, name := range eng.Expired() {
		msg := fmt.Sprintf("rule %q is past its expires date and no longer matches", name)
		rep.Warnings = append(rep.Warnings, msg)
		d.Logger.WarnContext(cmd.Context(), "ownership rule expired", "rule", name)
	}
	if err = sink.Write(func(w io.Writer, f render.Format) error {
		return writeScan(w, d, f, rep, snap.Problems)
	}); err != nil {
		return err
	}
	if n := scan.Gate(findings, e.failOn, e.types); len(e.failOn) > 0 && n >= a.minCount {
		return &exitError{code: ExitFindings, err: fmt.Errorf("%d finding(s) in %s", n, strings.Join(a.failOn, ", "))}
	}
	return scanIncomplete(snap.Problems, snap.Inventory.Runs)
}

func newScanEnv(cmd *cobra.Command, d *app.Deps, cfg *config.Config, a scanArgs) (*scanEnv, error) {
	m, err := typemap.Load()
	if err != nil {
		return nil, err
	}
	e := &scanEnv{cmd: cmd, d: d, cfg: cfg, a: a, m: m, types: scan.TypeMatcher(a.types)}
	if len(a.types) > 0 {
		if e.cfn = e.types.CFNTypes(m); len(e.cfn) == 0 {
			return nil, usageError(fmt.Errorf("--%s %s matches no mapped type", flagTypes, strings.Join(a.types, ",")))
		}
	}
	e.layers = scanLayers(cfg)
	e.cc = ccTypes(cfg, e.layers, e.cfn)
	if e.failOn, err = parseFailOn(a.failOn); err != nil {
		return nil, err
	}
	return e, nil
}

// scanLayers returns the configured inventory layers.
func scanLayers(cfg *config.Config) []string {
	var out []string
	for l := range strings.SplitSeq(cmp.Or(cfg.Scan.Layers, inventory.LayersAuto), ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ccTypes returns the types the Cloud Control layer lists: the allowlist, else the selected types
// when the cc layer is named.
func ccTypes(cfg *config.Config, layers, types []string) []string {
	cc := slices.Clone(cfg.Scan.CloudControlTypes)
	if len(cc) == 0 && slices.ContainsFunc(layers, func(l string) bool { return l == "cc" || l == inventory.LayerCC }) {
		cc = slices.Clone(types)
	}
	slices.Sort(cc)
	return slices.Compact(cc)
}

// parseFailOn validates the --fail-on categories scan and whois share.
func parseFailOn(values []string) ([]scan.Category, error) {
	var out []scan.Category
	for _, f := range values {
		c := scan.Category(strings.TrimSpace(f))
		if c != scan.CategoryUnmanaged && c != scan.CategoryDoubleManaged {
			return nil, usageError(fmt.Errorf("--%s %q: want unmanaged or double-managed", flagFailOn, f))
		}
		out = append(out, c)
	}
	return out, nil
}

// stateLookup backs the in_state rule function once the state index is known.
type stateLookup struct{ ids map[string]bool }

func (s *stateLookup) index(ix *tfstate.Index) {
	s.ids = map[string]bool{}
	for i := range ix.Entries {
		s.ids[ix.Entries[i].Type+"\x00"+ix.Entries[i].ID] = true
	}
}

func (s *stateLookup) has(tfType, id string) bool { return s.ids[tfType+"\x00"+id] }

// ownershipEngine compiles the built-in packs, the config's rules and the --rules files. A rule
// error is a usage error, found before any AWS call.
func ownershipEngine(d *app.Deps, cfg *config.Config, files []string, inState func(string, string) bool) (
	*ownership.Engine, error,
) {
	rules := make([]ownership.Rule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules = append(rules, ownership.Rule{
			Name: r.Name, Category: ownership.Category(r.Category), Owner: r.Owner, Match: r.Match, Reason: r.Reason,
			Expires: r.Expires,
		})
	}
	for _, f := range files {
		more, err := readRulesFile(f)
		if err != nil {
			return nil, usageError(err)
		}
		rules = append(rules, more...)
	}
	neverHide := make([]ownership.Category, 0, len(cfg.Scan.NeverHide))
	for _, c := range cfg.Scan.NeverHide {
		neverHide = append(neverHide, ownership.Category(c))
	}
	p := cfg.Scan.Packs
	eng, err := ownership.New(ownership.Options{
		Packs: map[string]bool{
			ownership.PackAWSDefaults: p.AWSDefaults, ownership.PackAWSService: p.AWSService,
			ownership.PackOtherIaC: p.OtherIaC, ownership.PackDerived: p.Derived, ownership.PackEphemeral: p.Ephemeral,
		},
		Rules: rules, NeverHide: neverHide, Now: d.Clock, InState: inState,
	})
	if err != nil {
		return nil, usageError(err)
	}
	return eng, nil
}

// snapshot is a scan.Snapshot and whether it came from the cache.
type snapshot struct {
	*scan.Snapshot

	cached bool
}

// cacheKey hashes everything that changes what a scan collects. Rules aren't part of it.
func (e *scanEnv) cacheKey(disc discovery.Result) (string, error) {
	type unitKey struct {
		Unit    string
		Backend *discovery.Backend
		Target  discovery.Target
	}
	units := make([]unitKey, 0, len(disc.Units))
	for i := range disc.Units {
		u := &disc.Units[i]
		units = append(units, unitKey{Unit: u.Repo + "/" + u.Path, Backend: u.Backend, Target: u.Target})
	}
	index := ""
	if e.a.index != "" {
		st, err := os.Stat(e.a.index)
		if err != nil {
			return "", usageError(fmt.Errorf("read --%s: %w", flagIndex, err))
		}
		index = fmt.Sprintf("%s|%d|%s", e.a.index, st.Size(), st.ModTime().UTC().Format(time.RFC3339Nano))
	}
	return scan.Key(map[string]any{
		"version": buildinfo.Get().Version, "accounts": e.cfg.Accounts, "regions": e.cfg.Regions,
		"layers": e.layers, "types": e.cfn, "cloudcontrol": e.cc, "units": units, "index": index,
	})
}

func (e *scanEnv) cache() (*scan.Cache, error) {
	dir, err := config.CacheDirPath(e.d.Env.Lookup, runtime.GOOS)
	if err != nil {
		return nil, fmt.Errorf("find cache directory: %w", err)
	}
	ttl, err := time.ParseDuration(cmp.Or(e.cfg.Cache.TTL, "1h"))
	if err != nil {
		return nil, usageError(fmt.Errorf("cache.ttl %q: %w", e.cfg.Cache.TTL, err))
	}
	return &scan.Cache{Dir: dir, TTL: ttl, Now: e.d.Clock}, nil
}

// snapshot returns the cached collection when it is fresh, else collects and caches it.
func (e *scanEnv) snapshot(loader *accounts.Loader, disc discovery.Result) (*snapshot, error) {
	ctx := e.cmd.Context()
	key, err := e.cacheKey(disc)
	if err != nil {
		return nil, err
	}
	cache, err := e.cache()
	if err != nil {
		return nil, err
	}
	if !e.a.refresh {
		if s, ok := cache.Load(key); ok {
			e.d.Logger.InfoContext(ctx, "using cached inventory and state index", "created", s.Created)
			return &snapshot{Snapshot: s, cached: true}, nil
		}
	}
	s, err := e.collect(loader, disc)
	if err != nil {
		return nil, err
	}
	s.Key, s.Created = key, e.d.Clock()
	if err = cache.Save(s); err != nil {
		e.d.Logger.WarnContext(ctx, "could not cache the scan", "error", err)
	}
	return &snapshot{Snapshot: s}, nil
}

func (e *scanEnv) collect(loader *accounts.Loader, disc discovery.Result) (*scan.Snapshot, error) {
	s := &scan.Snapshot{Problems: []scan.Problem{}}
	for _, de := range disc.Errors {
		s.Problems = append(s.Problems, scan.Problem{Target: de.Repo, Message: de.Err.Error()})
	}
	accts, err := resolveAccounts(e.cmd, e.d, loader, e.cfg)
	if err != nil {
		return nil, err
	}
	if len(accts.Accounts) == 0 && len(accts.Errors) > 0 {
		return nil, noAccountsError(accts)
	}
	for _, ae := range accts.Errors {
		s.Problems = append(s.Problems, scan.Problem{
			Target: ae.Source, Message: ae.Err.Error(), Auth: errors.Is(ae.Err, accounts.ErrAuth),
		})
	}
	if s.Index, err = e.stateIndex(loader, accts, disc, s); err != nil {
		return nil, err
	}
	s.Inventory, err = collectLive(e.cmd, e.d, e.m, liveRequest{
		accounts: accts.Accounts, regions: func(a *accounts.Account) []string { return e.regions(a, disc) },
		layers: e.layers, types: e.cfn, cc: e.cc, refresh: e.a.refresh,
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// liveRequest is what collectLive lists.
type liveRequest struct {
	accounts []*accounts.Account
	regions  func(*accounts.Account) []string // empty lets the probe choose
	layers   []string
	types    []string // CloudFormation types; empty means every type
	cc       []string
	refresh  bool
}

// collectLive probes each account and runs the inventory layers, as scan and whois do.
func collectLive(cmd *cobra.Command, d *app.Deps, m *typemap.Map, req liveRequest) (*inventory.Result, error) {
	ctx := cmd.Context()
	prober, err := newProber(d, req.refresh)
	if err != nil {
		return nil, err
	}
	targets := make([]inventory.Target, 0, len(req.accounts))
	for _, acct := range req.accounts {
		rep, perr := prober.Probe(ctx, acct, req.regions(acct))
		if perr != nil {
			return nil, fmt.Errorf("probe account %s: %w", acct.ID, perr)
		}
		targets = append(targets, inventory.Target{Account: acct, Report: rep})
	}
	env := &inventory.Env{Map: m, Limits: inventory.NewLimiters(nil), Logger: d.Logger, CloudControlTypes: req.cc}
	res, err := inventory.NewCollector(inventory.Options{Env: env}).Collect(ctx, inventory.Request{
		Targets: targets, Layers: req.layers, Types: req.types,
	})
	if errors.Is(err, inventory.ErrUnknownLayer) {
		return nil, usageError(err)
	}
	if err != nil {
		return nil, fmt.Errorf("list live resources: %w", err)
	}
	return res, nil
}

// regions returns the regions to scan in acct: the configured regions, else the regions of the
// units that target the account. Empty lets the probe choose.
func (e *scanEnv) regions(acct *accounts.Account, disc discovery.Result) []string {
	if len(e.cfg.Regions) > 0 {
		return e.cfg.Regions
	}
	var out []string
	for i := range disc.Units {
		u := &disc.Units[i]
		if u.Target.Account == acct.ID && u.Target.Region != "" && !slices.Contains(out, u.Target.Region) {
			out = append(out, u.Target.Region)
		}
	}
	slices.Sort(out)
	return out
}

// stateIndex reads the --index file, else every unit's state. States that fail become problems.
func (e *scanEnv) stateIndex(
	loader *accounts.Loader,
	accts *accounts.Result,
	disc discovery.Result,
	s *scan.Snapshot,
) (*tfstate.Index, error) {
	if e.a.index != "" {
		return readIndex(e.a.index)
	}
	ix, problems, err := readStates(e.cmd, e.d, e.cfg, e.m, stateRequest{
		loader: loader, accts: accts, disc: disc, concurrency: e.a.concurrency,
	})
	s.Problems = append(s.Problems, problems...)
	return ix, err
}

// readIndex reads an index file written by inventory --save-index.
func readIndex(path string) (*tfstate.Index, error) {
	var env struct {
		Data *tfstate.Index `json:"data"`
	}
	if err := tfstate.ReadIndexFile(path, &env); err != nil {
		return nil, usageError(err)
	}
	if env.Data == nil {
		return nil, usageError(fmt.Errorf("--%s %s has no index", flagIndex, path))
	}
	return env.Data, nil
}

// stateRequest is what readStates reads.
type stateRequest struct {
	loader      *accounts.Loader
	accts       *accounts.Result // nil without configured accounts
	disc        discovery.Result
	concurrency int
}

// readStates reads every discovered unit's state and indexes it. States that fail become problems.
func readStates(cmd *cobra.Command, d *app.Deps, cfg *config.Config, m *typemap.Map, req stateRequest) (
	*tfstate.Index, []scan.Problem, error,
) {
	cwd, err := d.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("get working directory: %w", err)
	}
	reader := tfstate.NewReader(tfstate.Options{
		Client:      stateClient(newBackends(req.loader, req.accts, cfg, d)),
		WorkDir:     cwd,
		Concurrency: req.concurrency,
		TFPath:      cfg.Binaries.Terraform,
		Logger:      d.Logger,
	})
	states := reader.Read(cmd.Context(), req.disc.Units)
	var problems []scan.Problem
	for i := range states {
		if u := &states[i]; u.Status == tfstate.StatusError {
			problems = append(problems, scan.Problem{
				Target: stateLabel(u), Message: u.Error, Auth: errors.Is(u.Err, accounts.ErrAuth),
			})
		}
	}
	return match.Index(m, states), problems, nil
}

// scanIncomplete is the error for a scan that missed something: exit 4 when every problem was a
// credential failure and no layer listed anything, else exit 1.
func scanIncomplete(problems []scan.Problem, runs []inventory.Run) error {
	failed, auth := 0, 0
	for _, p := range problems {
		failed++
		if p.Auth {
			auth++
		}
	}
	listed := false
	for _, r := range runs {
		switch r.Status {
		case inventory.RunDenied, inventory.RunError, inventory.RunPartial:
			failed++
		case inventory.RunOK:
			listed = true
		case inventory.RunUnsupported, inventory.RunSkipped:
		}
	}
	if failed == 0 {
		return nil
	}
	err := fmt.Errorf("scan incomplete: %d problem(s) (see the notes above)", failed)
	if auth == failed && !listed {
		return &exitError{code: ExitAuth, err: err}
	}
	return err
}

func sortFindings(fs []scan.Finding) {
	rank := func(c scan.Category) int { return slices.Index(scan.Categories(), c) }
	slices.SortStableFunc(fs, func(a, b scan.Finding) int {
		return cmp.Or(
			strings.Compare(a.Account, b.Account), strings.Compare(a.Region, b.Region),
			cmp.Compare(rank(a.Category), rank(b.Category)), strings.Compare(a.Type(), b.Type()),
			strings.Compare(a.Key(), b.Key()),
		)
	})
}

func (e *scanEnv) writeExplain(
	format render.Format,
	results []match.Result,
	eng *ownership.Engine,
	ix *ownership.Index,
) error {
	want := e.a.explain
	if strings.HasPrefix(want, "arn:") {
		want = typemap.NormalizeARN(want)
	}
	for i := range results {
		r := &results[i].Resource
		if r.ARN == want || r.ID == want {
			ex := scan.Explain(&results[i], eng, e.m, ix)
			if format == render.FormatJSON {
				return render.JSON(e.d.Stdout, render.NewEnvelope(buildinfo.Get().Version, e.d.Clock(), ex))
			}
			return scan.WriteExplanation(e.d.Stdout, &ex)
		}
	}
	return fmt.Errorf("no live resource has the ARN or ID %q", e.a.explain)
}

func writeCoverage(d *app.Deps, format render.Format, cov *inventory.Coverage) error {
	if format == render.FormatJSON {
		return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), cov))
	}
	return inventory.WriteCoverage(d.Stdout, cov)
}
