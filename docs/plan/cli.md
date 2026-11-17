# tfcensus: CLI surface

| Command | Purpose | Release |
|---|---|---|
| `tfcensus doctor` | check binaries and gated features per [version-policy](../engineering/version-policy.md) (terragrunt ≥ 1.0, terraform ≥ 1.5 / tofu ≥ 1.6; exit 1 below a minimum, a missing binary is only reported), credentials per profile, and **capability probe** per account/region (Resource Explorer index/view, Config recorder/aggregator, Cloud Control) | v0.1 |
| `tfcensus units [paths…]` | discovered units with backend, account, region | v0.1 |
| `tfcensus inventory [paths…]` | state inventory: resources by type, TF/provider versions, serial, lineage, size, last modified | v0.1 |
| `tfcensus scan` | unmanaged detection across accounts/regions | v0.1 |
| `tfcensus audit backend` | S3 and lock-table hygiene + fleet checks | v0.1 |
| `tfcensus rules test <rules.yaml> [--resource file]` | evaluate ownership rules against samples | v0.1 |
| `tfcensus drift [paths…]` | fleet drift report (refresh-only plans) | v0.1 |
| `tfcensus whois <arn\|id>…` | which unit(s) manage it; double-managed check | v0.1 |
| `tfcensus codify --from scan.json --into <unit>` | emit `import` blocks (+ `.tfquery.hcl`) for unmanaged resources | v0.1 |
| `tfcensus move plan\|apply\|rollback\|mark-applied\|status <migration.hcl>` | state moves between units/accounts | v0.1 |
| `tfcensus move suggest --from <unit> --to <unit>…` | auto-detect moves from plans (own strict 1:1 engine, ADR-0016) → draft migration file | v0.3 |
| `tfcensus report html` | static single-file HTML from a scan/drift/audit JSON | v0.1 |
| `tfcensus mcp` | stdio MCP server, read-only | v0.1 |
| `tfcensus config show\|validate\|init\|path`, `cache info\|clear` (the probe and scan caches, one row each; `clear` deletes both), `completion`, `version` | standard | v0.1 |

## Selection flags (shared by most commands)

| Flag | Meaning |
|---|---|
| `--profile <glob>` (repeatable) | AWS profiles from `~/.aws/config`, e.g. `'acme-*'` |
| `--org [--org-role OrganizationAccountAccessRole] [--management-profile p]` | enumerate accounts via Organizations |
| `--account <id>` / `--exclude-account <id>` (repeatable) | `doctor`, `scan` and `whois`: the account IDs to list resources in (config `accounts.include` and `accounts.exclude`). `doctor` filters before it assumes any role; `scan` and `whois` still read state with the other accounts' credentials. `report`'s `--account` is a filter of the saved report instead |
| `--region <r>` (repeatable) / `--all-regions` | default: regions found in the units' backends and providers; `--all-regions` (`doctor` and `scan`, config `all_regions`) = the regions each account has enabled, from `account:ListRegions`. `whois` has `--region` only: it lists the answer's own region |
| `--filter <expr>` | passed through to `terragrunt --filter` for unit selection (paths, `name=`, git ranges) |
| `--types <glob>` | resource type filter (TF or CFN names) |
| `--layers re,config,cc,native` | force inventory layers (default: auto from capability probe) |
| `--rules <file>` | extra ownership rules |
| `--tf-path`, `--tg-path` | binaries (Terragrunt defaults to tofu, so tfcensus always passes the terraform binary: `TG_TF_PATH`, plus `--tf-path` where terragrunt accepts it) |

Output and global flags follow the [engineering standards](../engineering/engineering-standards.md): `--format tty|plain|md|json|csv|sarif|github`, a repeatable `--out <format>=<path>` on report commands (there is no global `-o`), `--fail-on`, `-v`, `--no-input`, `--yes` (move only).

## `--fail-on` values

Each command takes its own values. A match exits 3.

| Command | Values |
|---|---|
| `scan`, `whois` | `unmanaged`, `double-managed` (repeatable), counted against `--min-count`; `scan` also scopes by `--types` |
| `audit backend` | one severity, `info`, `low`, `medium` or `high`: any unsuppressed finding at or above it |
| `drift` | `drift`, `drift:reverts` (needs `--mode full`), `errors` (repeatable), counted against `--min-count` |

## Examples

```sh
tfcensus doctor --profile 'acme-*'
tfcensus units ~/src/infra/acme-live --format json
tfcensus scan --profile acme-nonprod --region eu-west-1 --types 'aws_security_group*,aws_iam_*'
tfcensus audit backend --profile 'globex-*' --fail-on high --out sarif=audit.sarif
tfcensus whois arn:aws:elasticloadbalancing:eu-west-1:111111111111:loadbalancer/net/public/abc
```
