package cli

import (
	"bufio"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
	"github.com/sassdavid/tfcensus/internal/whois"
)

const (
	flagType  = "type"
	flagStdin = "stdin"
	flagLive  = "live"
	flagRepo  = "repo"
)

type whoisArgs struct {
	queries     []string
	types       []string
	stdin       bool
	index       string
	live        bool
	repos       []string
	filters     []string
	rules       []string
	failOn      []string
	minCount    int
	concurrency int
}

func newWhoisCmd(d *app.Deps, g *globals) *cobra.Command {
	a := whoisArgs{}
	cmd := &cobra.Command{
		Use:   "whois <arn|id>...",
		Short: "Show which units manage an AWS resource",
		Long: `whois looks each ARN or TF resource id up in the state of every discovered unit, with the
match keys scan uses (identity, then ARN, then id), and prints the units, addresses, accounts and
regions that manage it, and the key that matched. More than one state means double-managed.

An ARN whose type the typemap maps is matched like a live resource; any other ARN, and a bare id,
is looked up across every type. --type narrows the lookup to TF types.

When no state manages the resource, the ownership rules explain why (other-iac, aws-managed,
ignored or unmanaged). Without --index, whois lists the resource's type in its account and region
through the scan inventory layers, so the rules see its tags and relations. With --index the run
stays offline and the rules see only what the ARN gives; --live lists anyway.

--index reads an index written by tfcensus inventory --save-index instead of reading every state.

The exit code is 3 when --fail-on finds at least --min-count answers in its categories, 1 when a
state read or a layer failed, 4 when every failure was a credential problem, and 0 otherwise.`,
		Example: `  tfcensus whois arn:aws:iam::111111111111:role/app --repo ~/src/infra/acme-live
  tfcensus whois --index index.json.zst sg-0123456789abcdef0 --type aws_security_group
  jq -r '.data.findings[].arn // empty' scan.json | tfcensus whois --stdin --index index.json.zst
  tfcensus whois --index index.json.zst --fail-on double-managed arn:aws:s3:::acme-assets`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.outFormat.Check(
				render.FormatTTY, render.FormatPlain, render.FormatMD, render.FormatJSON, render.FormatSARIF,
			); err != nil {
				return usageError(err)
			}
			if a.minCount < 1 {
				return usageError(fmt.Errorf("--%s must be at least 1, got %d", flagMinCount, a.minCount))
			}
			if a.concurrency < 1 {
				return usageError(fmt.Errorf("--%s must be at least 1, got %d", flagConcurrency, a.concurrency))
			}
			if !cmd.Flags().Changed(flagLive) {
				a.live = a.index == ""
			}
			queries, err := whoisQueries(d, args, a.stdin)
			if err != nil {
				return err
			}
			a.queries = queries
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			return runWhois(cmd, d, g.outFormat, &res.Config, a)
		},
	}
	addDiscoveryFlags(cmd, &a.filters)
	addAccountFlags(cmd, d)
	addRegionFlag(cmd)
	f := cmd.Flags()
	f.StringSliceVar(&a.types, flagType, nil, "TF resource types to look in, such as aws_security_group")
	f.BoolVar(&a.stdin, flagStdin, false, "read more ARNs or ids from stdin, one per line")
	f.StringVar(&a.index, flagIndex, "", "read the state index from this file (from inventory --save-index) "+
		"instead of reading states")
	f.BoolVar(&a.live, flagLive, true, "list an unmanaged resource's type live so the rules see its tags "+
		"(default true, false with --index)")
	f.StringArrayVar(&a.repos, flagRepo, nil, "repository to discover units in (repeatable; config discovery.roots, "+
		"else the working directory)")
	f.StringArrayVar(&a.rules, flagRules, nil, "YAML file with more ownership rules under rules: (repeatable)")
	f.StringSliceVar(
		&a.failOn,
		flagFailOn,
		nil,
		complete.Usage("exit 3 when answers are in these categories: {values}", findingGateChoices()),
	)
	f.IntVar(&a.minCount, flagMinCount, 1, "answers --fail-on needs before it exits 3")
	f.IntVar(&a.concurrency, flagConcurrency, tfstate.DefaultConcurrency, "states read at the same time")
	cmd.MarkFlagsMutuallyExclusive(flagIndex, flagRepo)
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagType:        complete.DynamicList(complete.Timeout, complete.TFTypes()),
		flagIndex:       savedFiles(),
		flagRepo:        complete.Dirs(),
		flagRules:       complete.Files("yaml", "yml"),
		flagFailOn:      complete.List(findingGateChoices()),
		flagMinCount:    complete.NoFiles,
		flagConcurrency: complete.NoFiles,
	})
	cmd.ValidArgsFunction = complete.NoFiles
	return cmd
}

// whoisQueries returns the arguments and, with --stdin, the non-empty lines of stdin that don't
// start with #, without duplicates.
func whoisQueries(d *app.Deps, args []string, stdin bool) ([]string, error) {
	out := slices.Clone(args)
	if stdin {
		sc := bufio.NewScanner(d.Stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20) //nolint:mnd // ARNs are short; 1 MiB per line is plenty
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
	}
	var uniq []string
	for _, q := range out {
		if !slices.Contains(uniq, q) {
			uniq = append(uniq, q)
		}
	}
	if len(uniq) == 0 {
		return nil, usageError(fmt.Errorf("give an ARN or id, or --%s", flagStdin))
	}
	return uniq, nil
}

func runWhois(cmd *cobra.Command, d *app.Deps, format render.Format, cfg *config.Config, a whoisArgs) error {
	failOn, err := parseFailOn(a.failOn)
	if err != nil {
		return err
	}
	m, err := typemap.Load()
	if err != nil {
		return err
	}
	inState := &stateLookup{}
	eng, err := ownershipEngine(d, cfg, a.rules, inState.has)
	if err != nil {
		return err
	}
	loader := newLoader(d)
	var accts *accounts.Result
	if len(cfg.Accounts.Profiles) > 0 || cfg.Accounts.Org.Enabled {
		if accts, err = resolveAccounts(cmd, d, loader, cfg); err != nil {
			return err
		}
	}
	ix, problems, err := whoisIndex(cmd, d, cfg, m, loader, accts, a)
	if err != nil {
		return err
	}
	inState.index(ix)
	rv := &whois.Resolver{Matcher: match.New(m, ix), Map: m, Engine: eng}
	queries := make([]match.Query, len(a.queries))
	answers := make([]whois.Answer, len(a.queries))
	for i, v := range a.queries {
		queries[i] = match.Query{Value: v, Types: a.types}
		answers[i] = rv.Resolve(&queries[i])
	}
	runs, more, err := whoisLiveStep(cmd, d, cfg, m, rv, loader, accts, queries, answers, a.live)
	if err != nil {
		return err
	}
	problems = append(problems, more...)
	rep := &whoisReport{Answers: answers, Warnings: []string{}}
	for _, name := range eng.Expired() {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("rule %q is past its expires date and no longer matches", name))
	}
	if err = writeWhois(d, format, rep, problems); err != nil {
		return err
	}
	return whoisGate(answers, failOn, a, problems, runs)
}

// whoisGate returns exit 3 when --fail-on counts at least --min-count answers, else the error for
// a run that missed something.
func whoisGate(
	answers []whois.Answer, failOn []scan.Category, a whoisArgs, problems []scan.Problem, runs []inventory.Run,
) error {
	findings := make([]scan.Finding, len(answers))
	for i := range answers {
		findings[i] = answers[i].Finding
	}
	if n := scan.Gate(findings, failOn, nil); len(failOn) > 0 && n >= a.minCount {
		return &exitError{code: ExitFindings, err: fmt.Errorf("%d answer(s) in %s", n, strings.Join(a.failOn, ", "))}
	}
	return scanIncomplete(problems, runs)
}

// whoisLiveStep runs the live lookup when it is on and an answer needs it, resolving the default
// account first when no account is configured.
func whoisLiveStep(
	cmd *cobra.Command, d *app.Deps, cfg *config.Config, m *typemap.Map, rv *whois.Resolver,
	loader *accounts.Loader, accts *accounts.Result, queries []match.Query, answers []whois.Answer, live bool,
) ([]inventory.Run, []scan.Problem, error) {
	if !live || !slices.ContainsFunc(answers, func(an whois.Answer) bool { return rv.NeedsLive(&an) }) {
		return nil, nil, nil
	}
	if accts == nil {
		var err error
		if accts, err = resolveAccounts(cmd, d, loader, cfg); err != nil {
			return nil, nil, err
		}
	}
	var problems []scan.Problem
	for _, ae := range accts.Errors {
		problems = append(problems, scan.Problem{
			Target: ae.Source, Message: ae.Err.Error(), Auth: errors.Is(ae.Err, accounts.ErrAuth),
		})
	}
	runs, err := whoisLive(cmd, d, cfg, m, rv, accts, queries, answers)
	return runs, problems, err
}

// whoisIndex reads the --index file, else discovers the units and reads their states.
func whoisIndex(
	cmd *cobra.Command, d *app.Deps, cfg *config.Config, m *typemap.Map, loader *accounts.Loader,
	accts *accounts.Result, a whoisArgs,
) (*tfstate.Index, []scan.Problem, error) {
	if a.index != "" {
		ix, err := readIndex(a.index)
		return ix, []scan.Problem{}, err
	}
	disc, err := discoverUnits(cmd, d, cfg, a.filters, a.repos, loader)
	if err != nil {
		return nil, nil, err
	}
	problems := []scan.Problem{}
	for _, de := range disc.Errors {
		problems = append(problems, scan.Problem{Target: de.Repo, Message: de.Err.Error()})
	}
	ix, more, err := readStates(cmd, d, cfg, m, stateRequest{
		loader: loader, accts: accts, disc: disc, concurrency: a.concurrency,
	})
	return ix, append(problems, more...), err
}

// livePlan is what whoisLive lists: the accounts, each account's regions, the types, and the
// ARNs it looks for.
type livePlan struct {
	targets []*accounts.Account
	regions map[string][]string
	types   []string
	wanted  map[string]bool
}

// planLive collects what the answers that need a live lookup ask for. An answer whose account
// has no credentials is marked so.
func planLive(rv *whois.Resolver, accts *accounts.Result, answers []whois.Answer) livePlan {
	byAccount := map[string]*accounts.Account{}
	for _, acct := range accts.Accounts {
		byAccount[acct.ID] = acct
	}
	p := livePlan{regions: map[string][]string{}, wanted: map[string]bool{}}
	for i := range answers {
		f := &answers[i].Finding
		if !rv.NeedsLive(&answers[i]) {
			continue
		}
		acct, ok := byAccount[f.Account]
		if !ok {
			answers[i].Live = whois.LiveNoCredentials
			continue
		}
		if !slices.Contains(p.targets, acct) {
			p.targets = append(p.targets, acct)
		}
		if f.Region != "" && !slices.Contains(p.regions[acct.ID], f.Region) {
			p.regions[acct.ID] = append(p.regions[acct.ID], f.Region)
		}
		if !slices.Contains(p.types, f.CFNType) {
			p.types = append(p.types, f.CFNType)
		}
		p.wanted[f.ARN] = true
	}
	slices.Sort(p.types)
	return p
}

// regionsFor returns the regions to list in an account: the answers' regions, else the configured
// regions, else the account's home region. A global type needs one region, not every enabled one.
func (p *livePlan) regionsFor(cfg *config.Config) func(*accounts.Account) []string {
	return func(a *accounts.Account) []string {
		r := slices.Clone(p.regions[a.ID])
		if len(r) == 0 {
			r = slices.Clone(cfg.Regions)
		}
		if len(r) == 0 && a.Config.Region != "" {
			r = []string{a.Config.Region}
		}
		slices.Sort(r)
		return r
	}
}

// whoisLive lists the types of the answers no state manages, one inventory run per account over
// their regions and types, and refines those answers with what it finds.
func whoisLive(
	cmd *cobra.Command, d *app.Deps, cfg *config.Config, m *typemap.Map, rv *whois.Resolver,
	accts *accounts.Result, queries []match.Query, answers []whois.Answer,
) ([]inventory.Run, error) {
	p := planLive(rv, accts, answers)
	if len(p.targets) == 0 {
		return nil, nil
	}
	layers := scanLayers(cfg)
	d.Logger.InfoContext(cmd.Context(), "listing unmanaged resources live", "resources", len(p.wanted),
		"accounts", len(p.targets), "types", len(p.types))
	inv, err := collectLive(cmd, d, m, liveRequest{
		accounts: p.targets, regions: p.regionsFor(cfg), layers: layers, types: p.types,
		cc: ccTypes(cfg, layers, p.types),
	})
	if err != nil {
		return nil, err
	}
	byARN := map[string]*inventory.CloudResource{}
	for i := range inv.Resources {
		byARN[typemap.NormalizeARN(inv.Resources[i].ARN)] = &inv.Resources[i]
	}
	oix := ownership.NewIndex(inv.Resources)
	for i := range answers {
		f := &answers[i].Finding
		if answers[i].Live != whois.LiveOffline || !p.wanted[f.ARN] {
			continue
		}
		listed := whois.Listed(inv.Coverage, f.Account, f.Region, f.CFNType)
		rv.Refine(&answers[i], &queries[i], byARN[typemap.NormalizeARN(f.ARN)], listed, oix)
	}
	return inv.Runs, nil
}
