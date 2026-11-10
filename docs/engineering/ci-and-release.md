# CI, release & distribution

## Local tasks (mise)

`mise.toml` pins `go`, `golangci-lint`, `goreleaser`, `gofumpt` and `govulncheck`, and defines these tasks:

| Task | Runs |
|---|---|
| `mise run check` | lint + test + vuln. **This is the single verification command in CLAUDE.md** |
| `mise run lint` | `golangci-lint run` |
| `mise run test` | `go test -race -count=1 ./...` |
| `mise run test:golden -- -update` | regenerates golden files (review the diff!) |
| `mise run docs` | regenerates `docs/cli/` via cobra/doc and fails if the checked-in copy differs |
| `mise run snapshot` | `goreleaser release --snapshot --clean` |

## GitHub Actions workflows

| Workflow | Trigger | Jobs |
|---|---|---|
| `ci.yml` | PR, push main | lint; test matrix (ubuntu, macos); docs-up-to-date; schema tests |
| `security.yml` | PR, weekly | govulncheck (SARIF), zizmor, dependency review |
| `live.yml` | nightly, manual | terraform/tofu/terragrunt e2e against moto (testcontainers), **no AWS account needed**. An optional real-AWS OIDC job is added only if a sandbox account ever exists |
| `release-please.yml` | push main | opens or updates the release PR (version bump + CHANGELOG.md from Conventional Commits) |
| `release.yml` | tag `v*` | goreleaser: binaries, checksums, SBOM, cosign keyless signature, attestations, Homebrew cask, container image |

Rules:
- Actions are pinned by commit SHA (Renovate updates them).
- `permissions:` start at `{}` and each job gets only what it needs. Release gets `id-token: write`, `attestations: write` and `contents: write`.
- Release tags are created only by release-please, never by hand.

## goreleaser essentials

- Targets: `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, `windows/{amd64,arm64}`, built with `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X main.version=..."`.
- `sboms:` via syft; `signs:` cosign `sign-blob --bundle` on the checksum file.
- `homebrew_casks:` (replaces `brews:`) publishes to the tap `sassdavid/homebrew-tap`.
- `nfpms:` (deb/rpm) is optional and comes later.
- `dockers_v2` is for CI use: image `ghcr.io/sassdavid/tfcensus` bundles nothing else. Users mount their own terragrunt and terraform.

## Distribution order

1. GitHub Releases (binaries + `install.sh`, verified with checksums).
2. Homebrew tap.
3. `mise use github:sassdavid/tfcensus` works on day one with no registry entry.
4. aqua-registry PR (`aqua:sassdavid/tfcensus`), followed by a mise registry PR (`registry/tfcensus.toml`) once there's some adoption.
5. `go install github.com/sassdavid/tfcensus/cmd/tfcensus@latest`, documented as unsigned.

## Versioning

- SemVer. In `0.x`, a minor release may break things, and each break is called out in the changelog under "⚠ BREAKING".
- The JSON output and config schemas carry their own `schema_version` that changes independently of the binary version.
- Support policy: the latest minor only, until 1.0.
