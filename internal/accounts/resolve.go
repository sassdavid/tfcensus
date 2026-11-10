package accounts

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"golang.org/x/sync/errgroup"
)

// DefaultOrgRole is the role Organizations creates in every account it creates.
const DefaultOrgRole = "OrganizationAccountAccessRole"

// Selection says which accounts to load. With no profiles and no org, it is the default chain.
type Selection struct {
	Profiles          []string          // globs over the shared config files
	Org               bool              // list the accounts of the organization
	ManagementProfile string            // lists the accounts; empty means the default chain
	OrgRole           string            // assumed in each member account; empty means [DefaultOrgRole]
	OURoles           map[string]string // OU or root ID to the role assumed in its direct member accounts
	Names             map[string]string // account ID to display name
}

// SourceError is an account source that failed. The other sources are still loaded.
type SourceError struct {
	Source  string // the profile, or "org" for the account listing
	Kind    Kind
	Account string // set for an organization member
	Name    string // the member's name in the organization
	Err     error
}

func (e *SourceError) Error() string { return e.Err.Error() }

func (e *SourceError) Unwrap() error { return e.Err }

// Result lists the loaded accounts, one per account ID, and the sources that failed.
type Result struct {
	Accounts []*Account
	Errors   []*SourceError
}

// Account returns the loaded account with the given ID, or nil.
func (r *Result) Account(id string) *Account {
	for _, a := range r.Accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// Resolve loads every account sel names. It fails only for a selection that can't be right,
// such as a glob that matches no profile; failures of single sources are in Result.Errors.
func (l *Loader) Resolve(ctx context.Context, sel Selection) (*Result, error) {
	res := &Result{}
	if len(sel.Profiles) > 0 {
		if err := l.resolveProfiles(ctx, sel.Profiles, res); err != nil {
			return nil, err
		}
	}
	if sel.Org {
		l.resolveOrg(ctx, sel, res)
	}
	if len(sel.Profiles) == 0 && !sel.Org {
		acct, err := l.Default(ctx)
		if errors.Is(err, ErrNoProfile) {
			return nil, err
		}
		res.add(acct, err, SourceError{Source: string(KindDefault), Kind: KindDefault})
	}
	res.finish(sel.Names)
	return res, nil
}

func (r *Result) add(acct *Account, err error, e SourceError) {
	if err != nil {
		e.Err = err
		r.Errors = append(r.Errors, &e)
		return
	}
	r.Accounts = append(r.Accounts, acct)
}

// finish keeps the first source for each account and sorts by account ID.
func (r *Result) finish(names map[string]string) {
	seen := map[string]bool{}
	accounts := r.Accounts[:0]
	for _, a := range r.Accounts {
		if seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		if n := names[a.ID]; n != "" {
			a.Name = n
		}
		accounts = append(accounts, a)
	}
	r.Accounts = accounts
	slices.SortStableFunc(r.Accounts, func(a, b *Account) int { return strings.Compare(a.ID, b.ID) })
	slices.SortStableFunc(r.Errors, func(a, b *SourceError) int {
		return cmp.Or(strings.Compare(a.Source, b.Source), strings.Compare(a.Account, b.Account))
	})
}

func (l *Loader) resolveProfiles(ctx context.Context, globs []string, res *Result) error {
	all, err := l.Profiles()
	if err != nil {
		return err
	}
	matched, err := MatchProfiles(globs, all)
	if err != nil {
		return err
	}
	accts := make([]*Account, len(matched))
	errs := make([]error, len(matched))
	g := new(errgroup.Group)
	g.SetLimit(l.opts.Concurrency)
	for i, p := range matched {
		g.Go(func() error {
			accts[i], errs[i] = l.Profile(ctx, p.Name)
			return nil
		})
	}
	_ = g.Wait() // failures are per profile, in errs
	for i, p := range matched {
		res.add(accts[i], errs[i], SourceError{Source: p.Name, Kind: KindProfile})
	}
	return nil
}

// OrgAccount is an active account of the organization.
type OrgAccount struct {
	ID     string
	Name   string
	Parent string // the OU or root ID; set only when OU roles are configured
}

func (l *Loader) resolveOrg(ctx context.Context, sel Selection, res *Result) {
	var mgmt *Account
	var err error
	if sel.ManagementProfile != "" {
		mgmt, err = l.Profile(ctx, sel.ManagementProfile)
	} else {
		mgmt, err = l.Default(ctx)
	}
	if err != nil {
		res.add(nil, err, SourceError{Source: "org", Kind: KindOrg})
		return
	}
	members, err := l.OrgAccounts(ctx, mgmt, len(sel.OURoles) > 0)
	if err != nil {
		res.add(nil, err, SourceError{Source: "org", Kind: KindOrg})
		return
	}
	accts := make([]*Account, len(members))
	errs := make([]error, len(members))
	g := new(errgroup.Group)
	g.SetLimit(l.opts.Concurrency)
	for i, m := range members {
		g.Go(func() error {
			accts[i], errs[i] = l.orgMember(ctx, mgmt, m, orgRole(sel, m.Parent))
			return nil
		})
	}
	_ = g.Wait() // failures are per account, in errs
	for i, m := range members {
		res.add(accts[i], errs[i], SourceError{Source: mgmt.Source, Kind: KindOrg, Account: m.ID, Name: m.Name})
	}
}

func orgRole(sel Selection, parent string) string {
	if r := sel.OURoles[parent]; r != "" {
		return r
	}
	return cmp.Or(sel.OrgRole, DefaultOrgRole)
}

func (l *Loader) orgMember(ctx context.Context, mgmt *Account, m OrgAccount, role string) (*Account, error) {
	if m.ID == mgmt.ID {
		// The management account has no OrganizationAccountAccessRole; its own credentials work.
		acct := *mgmt
		acct.Kind, acct.Name = KindOrg, m.Name
		return &acct, nil
	}
	assumed, err := l.AssumeRole(ctx, mgmt, Role{ARN: RoleARN(mgmt, m.ID, role)})
	if err != nil {
		return nil, err
	}
	acct := *assumed
	acct.Kind, acct.Name = KindOrg, m.Name
	return &acct, nil
}

// OrgAccounts lists the active accounts of mgmt's organization, sorted by ID. With parents, it
// also looks up each account's direct parent OU.
func (l *Loader) OrgAccounts(ctx context.Context, mgmt *Account, parents bool) ([]OrgAccount, error) {
	client := organizations.NewFromConfig(mgmt.Config)
	var out []OrgAccount
	p := organizations.NewListAccountsPaginator(client, &organizations.ListAccountsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list organization accounts: %w", err)
		}
		for _, a := range page.Accounts {
			if active(a) {
				out = append(out, OrgAccount{ID: aws.ToString(a.Id), Name: aws.ToString(a.Name)})
			}
		}
	}
	slices.SortFunc(out, func(a, b OrgAccount) int { return strings.Compare(a.ID, b.ID) })
	if !parents {
		return out, nil
	}
	for i := range out {
		resp, err := client.ListParents(ctx, &organizations.ListParentsInput{ChildId: aws.String(out[i].ID)})
		if err != nil {
			return nil, fmt.Errorf("list parents of %s: %w", out[i].ID, err)
		}
		if len(resp.Parents) > 0 {
			out[i].Parent = aws.ToString(resp.Parents[0].Id)
		}
	}
	return out, nil
}

func active(a types.Account) bool {
	if a.State != "" {
		return a.State == types.AccountStateActive
	}
	return a.Status == types.AccountStatusActive
}

// BaseFor returns the credentials that own account id, for chaining a backend role: the profile
// that mapping names for it, else a loaded account with that ID, else the default chain.
func (l *Loader) BaseFor(ctx context.Context, id string, res *Result, mapping map[string]string) (*Account, error) {
	if p := mapping[id]; p != "" {
		return l.Profile(ctx, p)
	}
	if res != nil {
		if a := res.Account(id); a != nil {
			return a, nil
		}
	}
	return l.Default(ctx)
}

// Backend returns the credentials for reading a unit's state: base, or role assumed from base
// when the backend names one.
func (l *Loader) Backend(ctx context.Context, base *Account, role Role) (*Account, error) {
	if role.ARN == "" {
		return base, nil
	}
	return l.AssumeRole(ctx, base, role)
}
