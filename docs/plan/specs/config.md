# Spec: configuration

Files: user `$XDG_CONFIG_HOME/tfcensus/config.yaml`, project `.tfcensus.yaml` (repo root). Precedence and
provenance follow the [engineering standards](../../engineering/engineering-standards.md#configuration).

```yaml
schema_version: 1
binaries: { terragrunt: terragrunt, terraform: terraform }   # or tofu
discovery:
  roots: [~/src/infra]
  generate_stacks: false
  render: auto            # auto | always | never (fast path only)
accounts:
  profiles: ["acme-*", "globex-*"]
  org: { enabled: false, management_profile: "", role: OrganizationAccountAccessRole }
  map: { "111111111111": { profile: acme-nonprod, name: acme-nonprod } }
  aggregator: { profile: audit, name: aws-controltower-GuardrailsComplianceAggregator, region: eu-central-1 }
regions: [eu-west-1, eu-central-1, us-east-1]
scan:
  layers: auto
  cloudcontrol_types: ["AWS::ElasticLoadBalancingV2::Listener"]
  packs: { aws-defaults: true, aws-service: true, other-iac: true, derived: true, ephemeral: true }
  never_hide: [unmanaged-iam-users]
rules: []                 # see ownership-rules.md
audit:
  policy: { min_terraform_version: "1.10", noncurrent_days: 30, stale_lock_hours: 24 }
  suppress: []
drift:
  mode: refresh-only
  collapse: shape
  never_hide: [delete, replace]
  noise: []               # see drift.md
notify:                   # see notifications-and-reports.md and docs/runbooks/notify.md
  enabled: []
  categories: [double-managed, unmanaged]
  min_severity: low
  top: 10
  slack: { webhook_env: SLACK_WEBHOOK_URL, when: findings, link: "" }
  github_issues: { repo: "", api_url: "", token_env: GITHUB_TOKEN, labels: [], max_open: 10, close_resolved: true, summary: false }
  bitbucket_pr_comment: { workspace: "", repo: "", pull_request: 0, api_url: https://api.bitbucket.org/2.0, token_env: BITBUCKET_TOKEN, email_env: "" }
  file: { dir: ., formats: [md, json] }
cache: { ttl: 1h }
```

Rules:
- Unknown keys are an error.
- Credentials are never stored in config.
- Every list supports `expires` + `reason` where suppression is involved.

## Acceptance criteria
- [x] Schema generation and sync test; `config show --sources` golden; unknown-key error with file:line
  (`TestSpec_config_1` to `_3`).
