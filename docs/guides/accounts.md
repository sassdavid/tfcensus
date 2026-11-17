---
description: "Pick the AWS accounts tfcensus reads: profiles and globs, an AWS Organization with one role or a role per OU, accounts.map for odd profile names, and include and exclude lists."
---

# Choose accounts

tfcensus needs credentials for two kinds of work. It lists live resources in the accounts you
select, and it reads each unit's state from its S3 backend, which can sit in another account. This
page shows how to select accounts and how tfcensus finds the credentials for a backend.

`doctor`, `inventory`, `scan`, `whois` and `audit backend` take the same account flags. Each flag
has a config key, so a team can keep the selection in `.tfcensus.yaml`.

## The default credentials

Without `--profile` or `--org`, tfcensus uses the AWS SDK's default chain: `AWS_PROFILE`, then
environment credentials, then an instance or task role. That is one account, which is enough for a
first run:

```sh
AWS_PROFILE=acme-nonprod tfcensus doctor
```

## Profiles

`--profile` names a profile from `~/.aws/config` (or `AWS_CONFIG_FILE`). It is repeatable and takes
globs, so one flag can select a fleet:

```sh
tfcensus doctor --profile 'acme-*'
tfcensus scan --profile acme-nonprod --profile acme-prod
```

tfcensus calls `sts:GetCallerIdentity` for each profile to learn its account. When two profiles
reach the same account, the first one in name order is used. A glob that matches no profile is an
error, so a typo can't skip accounts without a word.

The config key is `accounts.profiles`:

```yaml
accounts:
  profiles: ["acme-*"]
```

## An AWS Organization

`--org` lists the active accounts of the organization and assumes a role in each member account.
The listing runs with `--management-profile` (`accounts.org.management_profile`), or with the
default credentials when it is empty. The role is `OrganizationAccountAccessRole` unless
`--org-role` (`accounts.org.role`) names another:

```sh
tfcensus scan --org --management-profile acme-management --org-role tfcensus-readonly
```

The management account itself has no such role, so tfcensus uses the management credentials there.

When OUs use different roles, map an OU or root ID to the role for the accounts directly under it
with `accounts.org.ou_roles`. An account whose direct parent has no entry gets `accounts.org.role`.
The map only has a config key, because maps can't be set from flags or environment variables.

```yaml
accounts:
  org:
    enabled: true
    management_profile: acme-management
    role: tfcensus-readonly
    ou_roles:
      ou-abcd-11111111: security-audit
      r-abcd: tfcensus-readonly
```

With `ou_roles` set, tfcensus also calls `organizations:ListParents` once per account.
`--profile` and `--org` work together: tfcensus loads both and keeps one source per account.

## Keep or drop accounts

`--account` keeps only the listed account IDs and `--exclude-account` drops some
(`accounts.include` and `accounts.exclude`). Both are repeatable:

```sh
tfcensus scan --org --exclude-account 222222222222
tfcensus doctor --profile 'acme-*' --account 111111111111
```

They decide where `doctor`, `scan` and `whois` list resources. `doctor` filters before it assumes
any role, so an excluded account costs no STS call. `scan` and `whois` still read the state of
units in an excluded account with that account's credentials, so the matching sees every state.
An ID in `accounts.include` that no source loaded gets a warning.

## Profile names that don't match accounts

Units name the account they deploy to, and the backend names its bucket and maybe a `profile` or
`role_arn`. When a state bucket lives in an account that none of your selected profiles reaches,
or you want reports to show another name for an account, map the account ID with `accounts.map`:

```yaml
accounts:
  map:
    "111111111111":
      profile: acme-nonprod
      name: acme-nonprod
    "222222222222":
      profile: acme-prod-readonly
      name: acme-prod
```

`name` is what reports show for the account. `profile` is the profile whose credentials reach it.
Quote the account IDs, so YAML reads them as strings.

## Credentials for a state backend

For each unit, tfcensus picks the credentials that read its state in this order:

1. The backend's own `profile`, when it names one.
2. The profile that `accounts.map` names for the bucket's account.
3. An account that `--profile` or `--org` loaded.
4. The default credentials.

When the backend has a `role_arn`, tfcensus assumes it from those credentials. The bucket's account
is the role's account, or the unit's target account when there is no role. The
[state concept page](/concepts/state) says more about how states are read.

## When credentials fail

A profile that fails, such as one with an expired SSO token, is skipped with a warning that says
what to run, such as `aws sso login --profile acme-prod`. The other accounts still run. When no
source works at all, the command fails, with exit 4 when a credential problem is among the causes.
A profile glob that matches nothing exits 2. An access denied error names the action, and the
[read-only IAM policy](/reference/iam) has every action tfcensus calls.

`tfcensus doctor` shows each source and whether it loaded, so run it first when you change the
selection. [Troubleshooting](/troubleshooting) covers the common errors.
