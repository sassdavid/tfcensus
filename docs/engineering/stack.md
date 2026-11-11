# Go stack

**Every version below was checked on 2026-11-09:**
- Go modules: queried `proxy.golang.org/<module>/@latest`, and probed `/vN+1` to catch new major versions.
- Go toolchain: `go.dev/dl`.
- GitHub Actions: their GitHub releases.

**Re-checked on 2026-11-10 at M0** (same method): every pinned version below was still the latest,
except three AWS modules that were one patch newer (updated in the table).
Re-run the check right before M0 (script: [§ Refresh](#refresh)). After M0, Renovate keeps everything current,
with a 3-day minimum release age (matching your mise `minimum_release_age` habit).

**Updated on 2026-11-11 at M5:** every direct and indirect Go module went to its `@latest` from
`proxy.golang.org`, queried one module at a time from the `go.mod` list. The owner waived the
3-day minimum age for this update, so the AWS modules released on 2026-11-10 are in. M5 added
`service/ec2`, `service/iam`, `service/route53` and `golang.org/x/time` at their latest versions.
relgap took the same bumps for the modules it shares.
Choices are justified in the [ADRs](adr/). Survey basis: go.mod files of gh, terragrunt, opentofu, trivy, syft, aqua,
goreleaser, chezmoi, k9s, golangci-lint.

## Toolchain (pinned in `mise.toml`)

| Item | Version | Note |
|---|---|---|
| Go | **1.27.4** toolchain; `go 1.27.0` directive per [version-policy.md](version-policy.md) | same as relgap |
| golangci-lint | **v2.15.0** | + golden config v2.15.0 ([ADR-0008](adr/0008-lint-config.md)) |
| gofumpt | **v0.12.0** | golines **v0.13.0** + goimports come with the golden config |
| govulncheck (`golang.org/x/vuln`) | **v1.9.0** | |
| go-licenses (`github.com/google/go-licenses/v2`) | **v2.0.1** | collects third-party notices for the archives and fails on forbidden, restricted or unknown licenses ([ADR-0012](adr/0012-third-party-notices.md)); gh pins the same version |
| goreleaser | **v2.20.0** | cosign **v3.1.4** |
| syft | **v1.57.1** | SBOMs for goreleaser `sboms:`, locally and in CI (installed by mise, so `anchore/sbom-action` isn't needed) |
| mise | **v2026.11.5** | pinned as `version:` of `jdx/mise-action` in workflows; `mise.lock` pins tool checksums |
| zizmor | **v1.32.0** | local workflow audit (`mise x zizmor@1.32.0 -- zizmor .github/workflows`); CI uses `zizmor-action`. `.github/zizmor.yml` turns off the `self-repository` audit until actionlint accepts `$/` |
| actionlint | **v1.7.12** | `mise run lint:actions`. Doesn't parse `uses: $/...` yet (rhysd/actionlint#711), so workflows use `./` |

## Libraries

| Concern | Module | Latest | Why / alternatives rejected |
|---|---|---|---|
| CLI | `github.com/spf13/cobra` (+ `spf13/pflag`) | v1.10.2 (+ v1.1.0) | [ADR-0002](adr/0002-cli-cobra.md) |
| Config | `github.com/knadh/koanf/v2` + `providers/env/v2` v2.0.2, `providers/posflag` v1.0.2 | v2.4.1 | [ADR-0003](adr/0003-config-koanf.md). `providers/file` v1.2.1 is not used: files are decoded with yaml v4 (node positions for file:line errors) and handed to koanf as maps |
| XDG paths | `github.com/adrg/xdg` | v0.5.3 | stable; used by aqua, syft, k9s |
| Logging | `log/slog` + `github.com/lmittmann/tint` | stdlib / v1.2.1 | zap rejected ([ADR-0004](adr/0004-logging.md)) |
| Concurrency | `golang.org/x/sync` (errgroup) | **v0.25.0** | |
| HTTP retry | `github.com/hashicorp/go-retryablehttp` | v0.7.8 | latest; used by terraform, tofu, trivy |
| Rate limit | `golang.org/x/time` | v0.16.0 | `rate.Limiter` per account, region and service for inventory layers |
| Compression | `github.com/klauspost/compress` (zstd: `inventory --save-index *.zst`, cache files) | v1.20.4 | gzip from the stdlib for `.gz`; zstd is what whois.md names |
| Styling | `charm.land/lipgloss/v2` (+ `/table`) | **v2.1.0** (released today) | latest at M0; gh/terragrunt/goreleaser are on v2.0.x |
| Markdown to terminal | `charm.land/glamour/v2` | v2.0.1 | |
| TTY | `github.com/mattn/go-isatty` | v0.0.24 | |
| Prompts | stdlib `bufio` → `charm.land/huh/v2` later | — / v2.0.3 | [ADR-0007](adr/0007-interactive-prompts.md) |
| JSON Schema validate | `github.com/santhosh-tekuri/jsonschema/v6` | v6.0.3 | |
| JSON Schema generate | `github.com/invopop/jsonschema` | v0.14.0 | config struct → schema; test-only (the schema sync test generates `schemas/config-v1.json`) |
| YAML | `go.yaml.in/yaml/v4` | **v4.0.0** (2026-11-04) | YAML org's maintained successor of `gopkg.in/yaml.v3`. If v4.0.0 shows early bugs at M0, fall back to `go.yaml.in/yaml/v3` v3.0.6 |
| TOML | `github.com/pelletier/go-toml/v2` | v2.4.3 | |
| MCP | `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | [ADR-0011](adr/0011-mcp-go-sdk.md) |

## Testing

| Concern | Module | Latest |
|---|---|---|
| Assertions | `github.com/stretchr/testify` | v1.12.1 |
| Diffs | `github.com/google/go-cmp` | v0.7.0 |
| CLI scripts | `github.com/rogpeppe/go-internal` (testscript) | v1.16.0 |
| Mocks | `go.uber.org/mock` | v0.6.0 |
| HTTP record/replay | `gopkg.in/dnaeon/go-vcr.v4` | v4.0.7 |
| In-process S3 fake | `github.com/johannesboyne/gofakes3` | v1.2.0 (test-only: state-read tests, the 500-state benchmark and writing the inventory cassette) |
| In-process AWS emulator | `github.com/sivchari/kumo` | v0.33.1 |
| Containers in tests | `github.com/testcontainers/testcontainers-go` | v0.45.0, running `motoserver/moto:5.2.5` and Floci 2.4.0 ([testing plan](../plan/testing.md)) |

## AWS, Terraform and rules

| Concern | Module | Latest |
|---|---|---|
| AWS core | `github.com/aws/aws-sdk-go-v2` · `/config` · `/credentials` · `github.com/aws/smithy-go` | v1.48.0 · v1.33.18 · v1.21.0 · v1.28.9 |
| AWS services | `service/sts` v1.52.0 · `s3` v1.114.10 · `organizations` v1.62.8 · `resourceexplorer2` v1.33.12 · `configservice` v1.74.12 · `cloudcontrol` v1.38.12 · `ec2` v1.344.3 · `iam` v1.65.0 · `route53` v1.70.10 · `dynamodb` v1.74.0 · `kms` v1.62.6 · `elasticloadbalancingv2` v1.64.0 · `cloudtrail` v1.65.12 · `s3control` v1.80.1 | released together 2026-11-10 (the 3-day minimum age was waived at M5). `ec2`, `iam` and `route53` are new at M5, for the native inventory listers. `cloudcontrol` and `elasticloadbalancingv2` aren't imported yet |
| Plan/schema structs | `github.com/hashicorp/terraform-json` | v0.29.0 |
| Exec terraform | `github.com/hashicorp/terraform-exec` | v0.26.0 |
| HCL read/write | `github.com/hashicorp/hcl/v2` (+ `hclwrite`) | v2.25.0 |
| cty | `github.com/zclconf/go-cty` | v1.20.0 |
| Rules | `github.com/google/cel-go` | v0.30.0 |
| Move auto-detect | `github.com/busser/tfautomv` (`pkg/engine`) | v0.7.0 (latest; low activity, so vendor-or-import is decided at M9) |

**Never import:** `hashicorp/terraform` (BSL 1.1), terragrunt `internal/...`, or OpenTofu `internal/` ([tfcensus ADR-0001](../plan/adr/0001-shell-out-to-terragrunt.md)).

## GitHub Actions (pin by commit SHA; versions as of 2026-11-09)

| Action | Latest |
|---|---|
| actions/checkout | v7.0.1 |
| actions/setup-go | v7.0.0 (or `jdx/mise-action` v5.2.7, which installs the whole mise toolchain; preferred) |
| golangci/golangci-lint-action | v9.3.0 |
| goreleaser/goreleaser-action | v7.3.0 |
| sigstore/cosign-installer | v4.1.3 |
| actions/attest | v4.2.2 |
| anchore/sbom-action | v0.24.3 |
| googleapis/release-please-action | v5.0.0 |
| github/codeql-action (SARIF upload) | v4.39.3 (bundle v2.27.4) |
| actions/dependency-review-action | v5.0.0 |
| zizmorcore/zizmor-action | v0.8.1 |

Indirect modules are whatever MVS selects, except where govulncheck flags a known (even unreachable)
vulnerability: at M0, `golang.org/x/text` went from v0.14.0 to v0.43.0 and `golang.org/x/sys` from
v0.28.0 to v0.49.0 (the newest releases older than the 3-day minimum age). At M5 every indirect
module went to its latest: `golang.org/x/text` v0.44.0, `x/mod` v0.43.0, `x/tools` v0.52.0,
`go.yaml.in/yaml/v3` v3.0.6, `buger/jsonparser` v1.6.1, `agext/levenshtein` v1.2.3,
`go-viper/mapstructure/v2` v2.5.0, `cpuguy83/go-md2man/v2` v2.0.7, `knadh/koanf/maps` v0.1.3,
`pb33f/ordered-map/v2` v2.3.2, and `pb33f/go-yaml` v0.1.1 (new, required by ordered-map v2.3.2).

| Concern | Module | Version | Why |
|---|---|---|---|
| Windows console colors | `golang.org/x/sys` (`windows`) | v0.50.0 | turns on VT processing so tint's ANSI colors render in conhost; already required by isatty |
| AWS SSO and sign-in credentials | `github.com/aws/aws-sdk-go-v2/service/sso` · `service/ssooidc` · `service/signin` | v1.39.5 · v1.44.5 · v1.13.1 | required by `config` for `sso-session`, legacy SSO and `aws login` profiles |
| AWS SDK internals | `aws-sdk-go-v2/feature/ec2/imds` · `internal/configsources` · `internal/endpoints/v2` · `internal/v4a` · `service/internal/accept-encoding` · `service/internal/endpoint-discovery` · `service/internal/presigned-url` · `service/internal/checksum` · `service/internal/s3shared` · `aws/protocol/eventstream` | v1.20.9 · v1.5.12 · v2.8.12 · v1.5.12 · v1.13.25 · v1.13.12 · v1.14.12 · v1.11.13 · v1.20.12 · v1.7.27 | required by `config` and the service clients; `endpoint-discovery` by `dynamodb`; the last three by `s3` |

At M2 the AWS SDK (core, `config`, `credentials`, smithy-go, `sts`, `organizations`,
`resourceexplorer2`, `configservice` at the versions in the AWS table) added only the AWS modules above.
It moved no module that relgap also uses, so relgap's go.mod needed no change (`go list -m all` in both
repos, compared on 2026-11-10). The AWS modules are Apache-2.0, and `mise run lint:licenses` passes.

## Typemap sources

`mise run typemap:gen` reads two provider checkouts, not Go modules. Neither is imported or
compiled in. The generated data records each checkout's version and commit in its header, and
ships with the providers' MPL-2.0 text as `internal/typemap/data/generated.yaml.LICENSE`
([ADR-0012](adr/0012-third-party-notices.md)).

| Source | Version at M5 | Read from |
|---|---|---|
| `hashicorp/terraform-provider-aws` | 6.72.1 (unreleased, commit `ec8b6ef16c52`) | `internal/service/*/service_package_gen.go`, `names/attr_consts_gen.go`, `internal/tags/tagpolicy/tagris-terraform-mapping.hcl` |
| `hashicorp/terraform-provider-awscc` | 1.109.1 (unreleased, commit `f886b4d9fe18`) | `internal/provider/all_schemas.hcl` |

## Refresh
The checker used for this table is in the session scratchpad. Re-create it in the repo as `scripts/latest-deps.py`
(it queries `proxy.golang.org/<module>/@latest` and probes `/vN+1`), or simply run `go get -u ./... && go list -m -u all`
once go.mod exists.
