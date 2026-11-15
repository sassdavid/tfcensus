# ADR-0015: Dependency updates with self-hosted Renovate

- Status: accepted (2026-11-15)

## Context
Until 2026-11-15 both tool repos and both action repos had a `renovate.json`, but no workflow ran
Renovate, so no dependency got updated. The first runs then showed what the old config missed:
`mise.lock` stayed stale, `prHourlyLimit` (default 2) held a weekly run to 2 PRs, lock file
maintenance never ran at 04:17 UTC, the `docs/ci/` examples and `testdata/` fixtures were in or out
of scope by accident, and security fixes on indirect modules got `chore`, which release-please skips.

## Decision
Every repo runs the same `.github/workflows/renovate.yml` as the owner's GitHub App
`sassdavid-renovate`. The file is shared by both tool repos, both action repos and
`sassdavid/action-test`, and `scripts/sibling-diff.sh` checks it.

- One workflow per repo, with a token for that repo only. The app key is a secret of the
  `renovate` environment, whose deployment branch is `main`.
- Mondays 01:17 UTC, inside the lock file maintenance window (Mondays before 04:00 UTC).
- `RENOVATE_PLATFORM_COMMIT=enabled`: commits through the API, signed, as the `main` ruleset
  requires. `RENOVATE_ALLOWED_UNSAFE_EXECUTIONS=["mise"]`, so `mise lock` updates `mise.lock`.
- `renovate-config.yml` runs `renovate-config-validator --strict` on every change to the config.

`renovate.json` extends `config:best-practices` and `:semanticCommits`, and sets:

- `minimumReleaseAge: 3 days`; the owner's own releases skip it: the tool pin and proselint in the
  action repos, and the relgap-action and tfcensus-action pins in action-test.
- `prHourlyLimit: 0`. `prConcurrentLimit` (default 10 open PRs) is the cap.
- One PR for non-major GitHub Actions updates and one for non-major mise tools. golangci-lint
  with its golden config (ADR-0008) and cosign keep their own groups. Majors get their own PR.
  action-test keeps one PR per mise tool, because its comment mode digests one tool per PR, and
  has no semantic commits or OSV alerts, because it releases nothing.
- Go: the `go` directive is never bumped (version-policy.md), `constraintsFiltering: strict`,
  `gomodTidy`, OSV alerts, and `vulnerabilityAlerts.semanticCommitType: fix`, so a security fix
  ships in a release.
- `ignorePaths` adds `**/testdata/**`; the github-actions manager also reads `docs/ci/*.yml`.
- The `renovate-version` input is aged by its npm release, because ghcr.io has no timestamps.

## Reasons
- A workflow per repo keeps each token to one repo, copies the setup proven in action-test and
  advent-of-code-rust, and follows the sibling rule of one shared file.
- `config:best-practices` is what Renovate's maintainers use: digest pins, config migration,
  abandonment notices and lock file maintenance.
- The 3 days come from engineering-standards.md. Weekly runs plus grouping keep the PR count low,
  and the Dependency Dashboard shows what the cooldown holds back.

## Consequences
- A new repo needs the app installed, the `renovate` environment with `RENOVATE_APP_PRIVATE_KEY`
  and `RENOVATE_APP_CLIENT_ID`, Issues and Dependabot alerts on ([ci-and-release.md](../ci-and-release.md#renovate)).
- A change to `renovate.yml` goes to all five repos; a change to `renovate.json` goes to the
  sibling repo too.
- Renovate updates its own version (`renovate-version` and `renovatebot/github-action`) in both
  workflows in one PR.
