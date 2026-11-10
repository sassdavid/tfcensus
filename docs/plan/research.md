# tfcensus: research record (2026-11-09)

## Prior art: unmanaged detection
| Tool | Status | Notes |
|---|---|---|
| snyk/driftctl (Go, Apache-2.0, ~2.7k★) | maintenance mode since 2023-06; last release v0.40.0 (2023-12) | ~103 enumerators, 108 AWS types, middlewares (expanders, defaults), `.driftignore`; executes provider binary 3.19 + TF 0.14 internals → broke on aws 5.x (#1671). Pain points: throttling (#1344), no multi-account (#1173), false positives (#1548, #1687, #530), CFN-owned (#878). No maintained fork |
| cloud-concierge (Go, ~250★) | last release 2025-01 | finds unmanaged resources and codifies them as a PR; stale |
| terracognita (Go) / Terraformer | 2023 release / archived 2025 | import tools, not coverage |
| Steampipe terraform + aws plugins | active | DIY SQL joins, no product |
| Terramate drift, tgsieve, driftive | active | managed-resource drift only |
| Firefly, ControlMonkey, Snyk IaC | commercial | Firefly categories: Codified/Drifted/Unmanaged/Ghost/IaC-Ignored/Child/Undetermined |
| `terraform query` (TF 1.14+, current 1.17.1) | active | per-config list resources (aws provider: 276 list resources); OpenTofu has no support yet (#3787) |

Demand: env0 "8 drift tools 2026", Scalr "find unmanaged resources" guide, a Medium "open source driftctl alternatives" post; driftctl issues #1700/#1716/#1717 after maintenance mode.

## Prior art: fleet features
- tfmigrate (MIT, v0.4.6 2026-10): migration HCL, multi_state, S3 history; issues #200, #220, #221, #217, #167, #202, #159, #161.
- tfautomv (Apache-2.0, v0.7.0 2025-06): importable `pkg/engine`, strict 1:1 delete/create matching.
- tgsieve (MIT, v0.13 2026-08): json-out-dir ingestion, cross-unit collapsing, noise rules with expiry/never_hide, md/json/github output.
- driftive (Apache-2.0, v0.34 2026-10): per-unit GitHub issues lifecycle, Slack, regex over text plans.
- Terragrunt v1.2.1 (2026-10-23): `find --json`, `--filter` syntax, run report schema, `--json-out-dir`, dependency outputs from state by default; `backend migrate` moves whole states only (#6350).
- Prowler: generic S3/DynamoDB checks, not state-aware (S3 TLS false pass #13226). Terraboard: server, not CLI.

## AWS API facts
- Cloud Control: 1,234 listable types (docs 2025-12); 439 need a parent model.
- Resource Explorer: ~706 types (docs) / **649 live** (non-prod member account, 2026-11-09); `ListResources` needs a view; 5 req/s; indexing up to 36 h.
- Config: ~718 types; advanced query/aggregator appear free (only CIs + rule evaluations billed; verify).
- Live probe of a non-prod Control Tower member account: RE LOCAL indexes in eu-central-1, eu-north-1, eu-west-1, us-east-1, no aggregator; Control Tower Config recorder CONTINUOUS + allSupported in eu-west-1/eu-central-1, none in us-east-1; no aggregator in the member account.

## Provider facts (terraform-provider-aws main, 6.72)
1,761 resources (1,240 SDK / 521 Framework); 578 with resource identity; 276 list resources.

## Licensing
Terraform core BSL 1.1 (never import). MPL-2.0: terraform-json, hcl/v2, terraform-exec, terraform-config-inspect, provider-aws, awscc, OpenTofu. MIT: go-cty, Terragrunt, tfmigrate. Apache-2.0: driftctl, tfautomv, driftive.
