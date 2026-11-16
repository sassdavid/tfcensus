package cli

import (
	"errors"
	"fmt"
	"maps"
	"runtime"
	"slices"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/scan"
)

const (
	flagProfile           = "profile"
	flagOrg               = "org"
	flagOrgRole           = "org-role"
	flagManagementProfile = "management-profile"
	flagRegion            = "region"
	flagAllRegions        = "all-regions"
	flagAccount           = "account"
	flagExcludeAccount    = "exclude-account"

	// annotationTargets marks a command whose --account selects accounts. report's --account
	// filters a saved report instead and must not reach accounts.include.
	annotationTargets = "tfcensus.targets"
)

// addAccountFlags adds the flags that select AWS accounts. Their values reach the command through
// the config (see flagKeys).
func addAccountFlags(cmd *cobra.Command, d *app.Deps) {
	f := cmd.Flags()
	f.StringArray(flagProfile, nil,
		"AWS profile from ~/.aws/config, or a glob such as 'acme-*' (repeatable; config accounts.profiles)")
	f.Bool(flagOrg, false,
		"list the accounts of the AWS organization and assume --org-role in each (config accounts.org.enabled)")
	f.String(flagOrgRole, accounts.DefaultOrgRole,
		"role assumed in each member account (config accounts.org.role)")
	f.String(flagManagementProfile, "",
		"profile that lists the organization's accounts, else the default AWS credentials "+
			"(config accounts.org.management_profile)")
	profiles := complete.Dynamic(complete.Timeout, complete.Profiles(d.Env.Lookup))
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagProfile:           profiles,
		flagManagementProfile: profiles,
		flagOrgRole:           complete.NoFiles,
	})
}

// addAccountFilterFlags adds --account and --exclude-account to a command that lists resources
// in each account. Their values reach the command through accounts.include and accounts.exclude.
func addAccountFilterFlags(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[annotationTargets] = "true"
	f := cmd.Flags()
	f.StringArray(flagAccount, nil, "keep only this account ID (repeatable; config accounts.include)")
	f.StringArray(flagExcludeAccount, nil, "drop this account ID (repeatable; config accounts.exclude)")
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagAccount:        complete.NoFiles,
		flagExcludeAccount: complete.NoFiles,
	})
}

// addRegionFlag adds --region, which reaches the command through the config key regions.
func addRegionFlag(cmd *cobra.Command) {
	cmd.Flags().StringArray(flagRegion, nil, "AWS region (repeatable; config regions)")
	_ = cmd.RegisterFlagCompletionFunc(flagRegion, complete.Enum(complete.Regions()))
}

// addAllRegionsFlags adds --region and --all-regions. [checkRegions] settles regions against
// all_regions after the config is loaded.
func addAllRegionsFlags(cmd *cobra.Command) {
	addRegionFlag(cmd)
	cmd.Flags().Bool(flagAllRegions, false,
		"use every region each account has enabled, from account:ListRegions (config all_regions)")
	cmd.MarkFlagsMutuallyExclusive(flagRegion, flagAllRegions)
}

// checkRegions lets --region and --all-regions override the other one's config key, and
// rejects a config that sets both regions and all_regions.
func checkRegions(cmd *cobra.Command, cfg *config.Config) error {
	f := cmd.Flags()
	switch {
	case f.Lookup(flagAllRegions) == nil:
		cfg.AllRegions = false
	case f.Changed(flagAllRegions) && cfg.AllRegions:
		cfg.Regions = nil
	case f.Changed(flagRegion):
		cfg.AllRegions = false
	}
	if cfg.AllRegions && len(cfg.Regions) > 0 {
		return usageError(errors.New("regions and all_regions are both set; " +
			"use one, or pass --region or --all-regions to override the config"))
	}
	return nil
}

// enabledRegions lists the enabled regions of each account when all_regions is set, else it
// returns nil. An account whose listing fails is in the second map instead.
func enabledRegions(
	cmd *cobra.Command,
	cfg *config.Config,
	accts []*accounts.Account,
) (map[string][]string, map[string]error) {
	if !cfg.AllRegions {
		return nil, nil
	}
	out := make(map[string][]string, len(accts))
	errs := map[string]error{}
	for _, a := range accts {
		regions, err := accounts.EnabledRegions(cmd.Context(), a)
		if err != nil {
			errs[a.ID] = err
			continue
		}
		out[a.ID] = regions
	}
	return out, errs
}

// regionProblems turns the accounts whose enabled regions couldn't be listed into scan problems.
func regionProblems(errs map[string]error) []scan.Problem {
	out := make([]scan.Problem, 0, len(errs))
	for _, id := range slices.Sorted(maps.Keys(errs)) {
		out = append(out, scan.Problem{
			Target: id, Message: errs[id].Error() + "; listed the default regions instead",
			Auth: errors.Is(errs[id], accounts.ErrAuth),
		})
	}
	return out
}

func newLoader(d *app.Deps) *accounts.Loader {
	return accounts.New(accounts.Options{Lookup: d.Env.Lookup, Transport: d.HTTPTransport, Logger: d.Logger})
}

func selection(cfg *config.Config) accounts.Selection {
	names := map[string]string{}
	for id, ref := range cfg.Accounts.Map {
		names[id] = ref.Name
	}
	return accounts.Selection{
		Profiles:          cfg.Accounts.Profiles,
		Org:               cfg.Accounts.Org.Enabled,
		ManagementProfile: cfg.Accounts.Org.ManagementProfile,
		OrgRole:           cfg.Accounts.Org.Role,
		OURoles:           cfg.Accounts.Org.OURoles,
		Names:             names,
	}
}

// targetSelection is [selection] with accounts.include and accounts.exclude, for a command that
// needs no other account's credentials.
func targetSelection(cfg *config.Config) accounts.Selection {
	sel := selection(cfg)
	sel.Include, sel.Exclude = cfg.Accounts.Include, cfg.Accounts.Exclude
	return sel
}

// liveTargets keeps the accounts that accounts.include and accounts.exclude select. scan and
// whois still read state with the credentials of the others.
func liveTargets(cmd *cobra.Command, d *app.Deps, cfg *config.Config, res *accounts.Result) []*accounts.Account {
	sel := targetSelection(cfg)
	out := make([]*accounts.Account, 0, len(res.Accounts))
	for _, a := range res.Accounts {
		if sel.Wants(a.ID) {
			out = append(out, a)
		}
	}
	warnUnmatched(cmd, d, cfg, res)
	return out
}

// warnUnmatched warns about each accounts.include ID that no account source loaded.
func warnUnmatched(cmd *cobra.Command, d *app.Deps, cfg *config.Config, res *accounts.Result) {
	for _, id := range cfg.Accounts.Include {
		if res.Account(id) == nil {
			d.Logger.WarnContext(cmd.Context(), "no account source loaded this account", "account", id)
		}
	}
}

// resolveAccounts loads the selected accounts and warns once about each source that failed.
func resolveAccounts(
	cmd *cobra.Command,
	d *app.Deps,
	l *accounts.Loader,
	cfg *config.Config,
) (*accounts.Result, error) {
	return resolve(cmd, d, l, selection(cfg))
}

func resolve(cmd *cobra.Command, d *app.Deps, l *accounts.Loader, sel accounts.Selection) (*accounts.Result, error) {
	res, err := l.Resolve(cmd.Context(), sel)
	if errors.Is(err, accounts.ErrNoProfile) {
		return nil, usageError(err)
	}
	if err != nil {
		return nil, fmt.Errorf("load aws accounts: %w", err)
	}
	for _, e := range res.Errors {
		attrs := []any{"source", e.Source}
		if e.Account != "" {
			attrs = append(attrs, "account", e.Account)
		}
		d.Logger.WarnContext(cmd.Context(), "skipping account source", append(attrs, "error", e.Err)...)
	}
	return res, nil
}

// noAccountsError is the error for a run in which every account source failed: exit 4 when a
// credential problem is among the causes.
func noAccountsError(res *accounts.Result) error {
	err := fmt.Errorf("no account source worked (%d failed; see the warnings above)", len(res.Errors))
	for _, e := range res.Errors {
		if errors.Is(e.Err, accounts.ErrAuth) {
			return &exitError{code: ExitAuth, err: err}
		}
	}
	return err
}

func newProber(d *app.Deps, refresh bool) (*probe.Prober, error) {
	dir, err := config.CacheDirPath(d.Env.Lookup, runtime.GOOS)
	if err != nil {
		return nil, fmt.Errorf("find cache directory: %w", err)
	}
	return probe.New(probe.Options{CacheDir: dir, Refresh: refresh, Clock: d.Clock, Logger: d.Logger}), nil
}
