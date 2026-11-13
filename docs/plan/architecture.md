# tfcensus: architecture

## Foundations (F1–F4) and features on top

```
            ┌──────────────── F1 Discovery ────────────────┐
 repos ───▶ │ terragrunt find/render --json · plain TF roots│──▶ []Unit
            │ · bucket scan fallback                        │
            └───────────────────────────────────────────────┘
            ┌──────────────── F2 Accounts & credentials ───┐
 profiles ─▶│ SSO profiles · Org accounts + role chain      │──▶ Unit.backend creds, Account→aws.Config
            │ · backend assume_role · capability probe      │
            └───────────────────────────────────────────────┘
            ┌──────────────── F3 State reader & index ─────┐
 S3 ──────▶ │ raw v4 JSON (no locks) · tofu-encrypted detect│──▶ StateIndex (ARN/ID/identity → unit,address)
            └───────────────────────────────────────────────┘
            ┌──────────────── F4 Plan runner & parser ─────┐
 tg run ──▶ │ run --all --json-out-dir · report-file · logs │──▶ []UnitPlan (terraform-json)
            └───────────────────────────────────────────────┘

 features:  units · inventory · audit backend   (F1–F3)          v0.1
            scan = Inventory layers + Matcher + Ownership rules   v0.1
            drift (F4) · whois (F3 index) · codify (scan+F3)       v0.1
            move (F1–F4 + hclwrite)                                v0.1
            notify / html / mcp (all)                              v0.1
```

## Packages (`internal/`)

| Package | Responsibility | Spec |
|---|---|---|
| `discovery` | units from terragrunt/plain TF; backend resolution | [specs/discovery.md](specs/discovery.md) |
| `accounts` | profiles, org accounts, role chains, `aws.Config` cache, capability probe | [specs/accounts-and-credentials.md](specs/accounts-and-credentials.md) |
| `tfstate` | raw state v4 model, S3 reader, encryption detection, `StateIndex` | [specs/state-reader.md](specs/state-reader.md) |
| `inventory` | live cloud inventory: `Layer` interface (resourceexplorer, config, cloudcontrol, native/*) | [specs/inventory.md](specs/inventory.md) |
| `typemap` | TF type ↔ CFN type ↔ ARN pattern ↔ ID normalizers (data-driven) | [specs/matching.md](specs/matching.md) |
| `match` | join live resources with StateIndex | [specs/matching.md](specs/matching.md) |
| `ownership` | ordered rules (built-in + user CEL) → category + reason | [specs/ownership-rules.md](specs/ownership-rules.md) |
| `audit` | backend and fleet checks | [specs/backend-audit.md](specs/backend-audit.md) |
| `plan` | F4: drive terragrunt, ingest plan JSON + run report, collapse, noise rules | [specs/drift.md](specs/drift.md) |
| `codify` | import / `.tfquery.hcl` generation via hclwrite | [specs/whois-and-codify.md](specs/whois-and-codify.md) |
| `migrate` | move: migration files, surgery/native strategies, history, rollback | [specs/move.md](specs/move.md) |
| `notify` | Slack / GitHub / Bitbucket notifiers | [specs/notifications-and-reports.md](specs/notifications-and-reports.md) |
| `render` | tty / plain / md / json / sarif / csv / html | [specs/scan.md](specs/scan.md) |
| `execx` | terragrunt / terraform / tofu exec wrapper | [adr/0001](adr/0001-shell-out-to-terragrunt.md) |
| `mcpserver` | read-only MCP tools | [specs/notifications-and-reports.md](specs/notifications-and-reports.md) |

## Core data model (sketch)

```go
type Unit struct {
    Path, Repo string            // live/prod/eu-west-1/eks
    Kind      UnitKind           // terragrunt | terraform | stack-generated
    Backend   Backend            // s3: bucket, key, region, role_arn, use_lockfile, dynamodb_table, workspace
    Account   string             // resolved target account (from provider/role/account.hcl), may be ""
    Region    string
}

type ManagedResource struct {
    Unit *Unit; Address, Type, Provider, Mode string // mode: managed|data (data skipped)
    Identity map[string]any; ARN, ID string; Account, Region string // derived from attrs/identity/provider
}

type CloudResource struct {
    Account, Region, CFNType, ARN, ID string
    TFTypes  []string           // candidate TF types from typemap
    Tags     map[string]string
    Layer    string             // resource-explorer | config | cloudcontrol | native:ec2-sg-rules
}

type Finding struct {
    Resource CloudResource; Category Category // managed|unmanaged|other-iac|aws-managed|derived|ignored|undetermined
    Owner, Reason, Rule string; ManagedBy []*ManagedResource // >1 = double-managed
}
```

## Execution model
- Read-only by default (an IAM policy is documented). Only `move apply` writes, and only to state ([adr/0007](adr/0007-read-only-by-default.md)).
- Fan-out: accounts (4) × regions (4) × layer pages. Adaptive retry. Each `aws.Config` is cached per profile/role.
- Results cache: `$XDG_CACHE_HOME/tfcensus/<account>/<region>/<layer>.json.zst` with a TTL (default 1 h), so a re-run with different filters or rules is instant (`--refresh` bypasses it).
- Determinism: findings are sorted by account, region, type, id.
