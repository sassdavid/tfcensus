# Go stack

**Every version below was checked on 2026-11-09:**
- Go modules: queried `proxy.golang.org/<module>/@latest`, and probed `/vN+1` to catch new major versions.
- Go toolchain: `go.dev/dl`.
- GitHub Actions: their GitHub releases.

**Re-checked on 2026-11-10 at M0** (same method): every pinned version below was still the latest,
except three AWS modules that were one patch newer (updated in the table).
Re-run the check right before M0 (script: [§ Refresh](#refresh)). After M0, Renovate keeps everything current,
with a 3-day minimum release age (matching your mise `minimum_release_age` habit).
Choices are justified in the [ADRs](adr/). Survey basis: go.mod files of gh, terragrunt, opentofu, trivy, syft, aqua,
goreleaser, chezmoi, k9s, golangci-lint.

## Toolchain (pinned in `mise.toml`)

| Item | Version | Note |
|---|---|---|
| Go | **1.27.3** toolchain; `go 1.27.0` directive per [version-policy.md](version-policy.md) | same as relgap |
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
| Rate limit | `golang.org/x/time` | v0.16.0 | |
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
| AWS core | `github.com/aws/aws-sdk-go-v2` · `/config` · `/credentials` · `github.com/aws/smithy-go` | v1.47.8 · v1.33.17 · v1.20.17 · v1.28.9 |
| AWS services | `service/sts` v1.51.9 · `s3` v1.114.9 · `organizations` v1.62.7 · `resourceexplorer2` v1.33.11 · `configservice` v1.74.11 · `cloudcontrol` v1.38.11 · `ec2` v1.344.2 · `iam` v1.64.9 · `route53` v1.70.9 · `dynamodb` v1.73.2 · `kms` v1.62.5 · `elasticloadbalancingv2` v1.63.9 | released together 2026-11-06 |
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
v0.28.0 to v0.49.0 (the newest releases older than the 3-day minimum age).

| Concern | Module | Version | Why |
|---|---|---|---|
| Windows console colors | `golang.org/x/sys` (`windows`) | v0.49.0 | turns on VT processing so tint's ANSI colors render in conhost; already required by isatty |
| AWS SSO and sign-in credentials | `github.com/aws/aws-sdk-go-v2/service/sso` · `service/ssooidc` · `service/signin` | v1.39.4 · v1.44.4 · v1.13.0 | required by `config` for `sso-session`, legacy SSO and `aws login` profiles |
| AWS SDK internals | `aws-sdk-go-v2/feature/ec2/imds` · `internal/configsources` · `internal/endpoints/v2` · `internal/v4a` · `service/internal/accept-encoding` · `service/internal/presigned-url` | v1.20.8 · v1.5.11 · v2.8.11 · v1.5.11 · v1.13.24 · v1.14.11 | required by `config` and the service clients |

At M2 the AWS SDK (core, `config`, `credentials`, smithy-go, `sts`, `organizations`,
`resourceexplorer2`, `configservice` at the versions in the AWS table) added only the AWS modules above.
It moved no module that relgap also uses, so relgap's go.mod needed no change (`go list -m all` in both
repos, compared on 2026-11-10). The AWS modules are Apache-2.0, and `mise run lint:licenses` passes.

## Refresh
The checker used for this table is in the session scratchpad. Re-create it in the repo as `scripts/latest-deps.py`
(it queries `proxy.golang.org/<module>/@latest` and probes `/vN+1`), or simply run `go get -u ./... && go list -m -u all`
once go.mod exists.
