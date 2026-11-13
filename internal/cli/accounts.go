package cli

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/probe"
)

const (
	flagProfile           = "profile"
	flagOrg               = "org"
	flagOrgRole           = "org-role"
	flagManagementProfile = "management-profile"
	flagRegion            = "region"
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

// addRegionFlag adds --region, which reaches the command through the config key regions.
func addRegionFlag(cmd *cobra.Command) {
	cmd.Flags().StringArray(flagRegion, nil, "AWS region (repeatable; config regions)")
	_ = cmd.RegisterFlagCompletionFunc(flagRegion, complete.Enum(complete.Regions()))
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

// resolveAccounts loads the selected accounts and warns once about each source that failed.
func resolveAccounts(
	cmd *cobra.Command,
	d *app.Deps,
	l *accounts.Loader,
	cfg *config.Config,
) (*accounts.Result, error) {
	res, err := l.Resolve(cmd.Context(), selection(cfg))
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
