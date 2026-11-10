# Reference repositories (local clones for implementation)

These are clones Claude Code (and you) can read during implementation, **for inspiration and verification only**.
They're never a dependency or a copy source:
- Never copy code from BSL-licensed repos (hashicorp/terraform).
- Ideas from MIT, Apache or MPL repos are fine. If code is adapted, keep its notice ([ADR-0010](../engineering/adr/0010-license-apache2.md)).
- Go module sources of actual dependencies are already in `$(go env GOMODCACHE)` after `go mod download`. Don't clone those just to read them.

**Location convention:** `~/src/github/_reference/<owner>/<repo>`. The script at the bottom does treeless partial clones of the default branch.
[CLAUDE.md](../../CLAUDE.md) points to this path (see "Reference sources").

## Already local (in `_reference` since 2026-11-09)

| Repo | Used for | Note |
|---|---|---|
| hashicorp/terraform-provider-aws | tfcensus typemap: resource identity schemas (`@IdentityAttribute` / identity kinds), `@SDKListResource`/`@FrameworkListResource`, ID formats, ARN patterns | full clone, `main` (≈ v6.72). `git pull` before running `typemap:gen` |
| hashicorp/terraform-provider-awscc | `internal/provider/all_schemas.hcl`: the CFN type universe and awscc↔CFN mapping | MPL-2.0. Read it as data |
| gruntwork-io/terragrunt-infrastructure-live-stacks-example | fixture inspiration for `testdata/repos/tg-live` (stacks, root.hcl) | |
| gruntwork-io/terragrunt-infrastructure-catalog-example | fixture inspiration (units/modules catalogue) | |
| terraform-aws-modules/* (21 repos) | realistic resource sets for fixture states (`terraform-aws-security-group`, `-eks`, `-iam`, `-s3-bucket`, `-vpc`, …) | |

`_reference` may also hold other local clones that this plan doesn't use.

## To clone

| Repo | License | Why | Priority |
|---|---|---|---|
| gruntwork-io/terragrunt | MIT | `find`/`render`/`info print` JSON shapes, `--filter` grammar, run report schema, `--json-out-dir`, remote_state handling (answers the M1 spike) | **M1, must** |
| hashicorp/terraform | **BSL 1.1: read only, never copy** | state v4 file format, plan JSON (`internal/command/jsonplan`), `query`/list blocks, S3 backend `use_lockfile` key layout | M3, must |
| opentofu/opentofu | MPL-2.0 | state encryption envelope (`encrypted_data`), differences in JSON output, S3 backend | M3, should |
| snyk/driftctl | Apache-2.0 | middleware catalogue (expanders, defaults), `.driftignore`, known false positives | M5, must |
| minamijoyo/tfmigrate | MIT | migration file grammar, multi_state flow, history, the override-backend trick | M9, must |
| busser/tfautomv | Apache-2.0 | `pkg/engine` move detection (possible import) | M9, should |
| imcitius/tgsieve | MIT | json-out-dir ingestion, collapse, noise rules with expiry/never_hide | M7, must |
| driftive/driftive | Apache-2.0 | issue lifecycle (create/update/close), Slack | M10, should |
| turbot/steampipe-plugin-aws | Apache-2.0 | aws-sdk-go-v2 multi-account/region fan-out, Config/Resource Explorer/Cloud Control usage, retry config | M2/M5, should |
| prowler-cloud/prowler | Apache-2.0 | S3/DynamoDB check logic (and its TLS-policy bug #13226 to avoid) | M4, nice |
| fujiwara/tfstate-lookup | MPL-2.0 | streaming raw state reading in Go | M3, nice |
| getmoto/moto | Apache-2.0 | `IMPLEMENTATION_COVERAGE.md`, emulator behaviour (L2 tests) | M0/M3, should |
| floci-io/floci | MIT | Resource Explorer + Cloud Control emulation (L2 tests) | M5, should |
| sivchari/kumo | MIT | in-process Go AWS emulator (L1 tests) | M2, nice |

## Go CLI patterns and template

| Repo | Why |
|---|---|
| cli/cli (gh) | cobra command structure, `Prompter` interface + huh accessible mode, acceptance tests with testscript, goreleaser + attestations workflow |
| goreleaser/goreleaser | reference `.goreleaser.yaml` (cosign bundle, SBOM, homebrew_casks), golangci config |
| maratori/golangci-lint-config | the golden `.golangci.yml` to copy at M0 |
| twpayne/chezmoi | heavy testscript usage, a good model for CLI golden tests |
| modelcontextprotocol/go-sdk | server examples (stdio, tools, schemas) |
| aquaproj/aqua | slog + tint, per-host HTTP handling, a large Go CLI |

## Clone script

```sh
# run once; safe to re-run (it pulls if the clone already exists)
REF=~/src/github/_reference
clone() { d="$REF/$1"; if [ -d "$d/.git" ]; then git -C "$d" pull --ff-only -q; else
  git clone -q --filter=tree:0 "https://github.com/$1.git" "$d"; fi; echo "ok $1"; }

# tfcensus
for r in gruntwork-io/terragrunt hashicorp/terraform opentofu/opentofu snyk/driftctl \
         minamijoyo/tfmigrate busser/tfautomv imcitius/tgsieve driftive/driftive \
         turbot/steampipe-plugin-aws prowler-cloud/prowler fujiwara/tfstate-lookup getmoto/moto floci-io/floci sivchari/kumo; do clone "$r"; done
# Go CLI patterns
for r in cli/cli goreleaser/goreleaser maratori/golangci-lint-config twpayne/chezmoi \
         modelcontextprotocol/go-sdk aquaproj/aqua; do clone "$r"; done

# already-local full clones: the same function just pulls them
for r in hashicorp/terraform-provider-aws hashicorp/terraform-provider-awscc \
         gruntwork-io/terragrunt-infrastructure-live-stacks-example \
         gruntwork-io/terragrunt-infrastructure-catalog-example; do clone "$r"; done
# refresh every clone in _reference, including ones not listed above
for g in "$REF"/*/*/.git; do git -C "${g%/.git}" pull --ff-only -q && echo "pulled ${g%/.git}"; done
```

`--filter=tree:0` (treeless) keeps the clones small while `git log` and `git blame` still work. terraform and
prowler are the largest. Clone only the "must" rows if disk or time matters.
