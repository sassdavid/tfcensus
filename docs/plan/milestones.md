# tfcensus: milestones

**One milestone = one fresh Claude Code session.** Start each session with:
"Read `CLAUDE.md`, then `docs/plan/milestones.md` M<n> and the linked specs. Implement M<n> only."
Tick a box only when its check has passed, and quote the evidence in the commit body.
Never run anything against real AWS accounts in a session unless the prompt explicitly allows a named read-only profile.

Release mapping (revised 2026-11-13): every release goes to GitHub only (archives, checksums,
SBOMs, the cosign bundle and attestations) until M19 turns the package channels on.
**v0.1.0 = M0-M11**, v0.2.0 = M12-M13, v0.3.0 = M15-M16, v0.4.0 = M17-M18, and M14 releases the
separate action repo. M19 has no fixed version or place in the order: the owner runs it when the
GitHub releases look right. M20 waits for external users. The research and decisions behind
M11-M20 are in [roadmap-research.md](roadmap-research.md).

---

### M0: Skeleton and pipeline
Same as [relgap M0](https://github.com/sassdavid/relgap/blob/main/docs/plan/milestones.md) (the shared template in [docs/engineering](../engineering/README.md)), plus `execx` with a fake-binary test harness, plus `docs/iam/tfcensus-readonly.json`.
- [x] `mise run check` is green. Snapshot release builds. CI is green (linux, macos and windows).
- [x] `tfcensus version --format json` validates against the envelope schema.

### M1: Discovery (F1) → [discovery](specs/discovery.md)
- [x] **Spike:** check whether `terragrunt render --json` (v1.2.1) includes `remote_state`. Record the answer and the chosen approach in [adr/0001](adr/0001-shell-out-to-terragrunt.md).
- [x] All discovery.md acceptance criteria. `tfcensus units` on fixtures, in tty and json.

### M2: Accounts and credentials (F2) → [accounts](specs/accounts-and-credentials.md)
- [x] Profile/SSO loading, Org mode, backend role chains, the expired-SSO UX. `tfcensus doctor` with the capability probe (stubbed).
- [x] *(Optional, manual, read-only on an existing work account, if employer policy allows)* `tfcensus doctor --profile <nonprod>` matches the facts recorded in the spec.

### M3: State reader and inventory (F3) → [state-reader](specs/state-reader.md)
- [x] All state-reader.md criteria. `tfcensus inventory` in json, md and csv. `--save-index`.

### M4: Backend audit → [backend-audit](specs/backend-audit.md)
- [x] Bucket, lock and fleet checks with fixtures. Suppressions. SARIF output.
- [x] *(Optional, manual)* Live validation recorded in `docs/validation/` (read-only, profiles named in the prompt).

### M5: Inventory layers and matching → [inventory](specs/inventory.md), [matching](specs/matching.md)
Scope: config + resource-explorer + native (sg-rules, iam-attachments, route53, s3-subresources) layers; typemap generation task + the first 40 types; the match algorithm.
- [x] inventory.md criteria 1–3, matching.md criteria 1–4.
- Not in M5: the Config aggregator fast path, the Cloud Control layer and its parent graph
  (inventory.md criterion 4), the 1 h results cache, and the emulator nightly. scan wires the
  layers and `--explain-coverage` at M6.

### M6: Ownership rules and scan report → v0.1.0 ([ownership](specs/ownership-rules.md), [scan](specs/scan.md))
- [x] All ownership-rules.md and scan.md criteria (emulator and fixture based). The manual
  false-positive measure (ownership criterion 5) is the optional item below.
- [ ] *(Optional, manual, strongly recommended before announcing v0.1)* A false-positive review on one real non-prod account, under 5% (recorded, aggregated only).
- [x] Cloud Control layer for allowlisted types with the parent graph (inventory criterion 4).
  The emulator nightly (inventory criterion 5) runs in `live.yml` and is ticked after its first pass.
- Moved to M11: the v0.1.0 release and its install checks.

### M7: Drift (F4) → v0.2.0 ([drift](specs/drift.md))
- [x] All drift.md criteria. A GitHub Actions example workflow in `docs/ci/`.

### M8: whois + codify → v0.3.0 ([whois-and-codify](specs/whois-and-codify.md))
- [x] All criteria. FL-DOUBLE audit check shares the whois index.

### M9: move → v0.4.0 ([move](specs/move.md))
- [x] All move.md criteria, including the cross-account e2e against moto (two emulated accounts via AssumeRole) with rollback.
- [x] `docs/runbooks/move.md`: the runbook a colleague can follow (generalizes the author's earlier hand-written migration runbooks).
- Not in M9: `move suggest` (tfautomv), recorded in move.md.

### M10: Notifications, HTML, MCP → v0.5.0 ([notifications-and-reports](specs/notifications-and-reports.md))
- [x] All criteria. The headless browser check of criterion 2 passed in CI (ci #43246311112 and #43247790958, ubuntu-latest).
- [x] The MCP server is registered in the author's Claude Code (local scope) and used read-only for a real whois and scan question: `docs/dogfood.md`.
- `docs/runbooks/notify.md`. `bitbucket-issues` is dropped (API removed by Atlassian), see
  [ADR-0012](adr/0012-notify-and-mcp-read-saved-reports.md).

---

From M11 on, tfcensus and relgap use the same milestone numbers. A milestone marked "shared" lands
in both repos in the same session (`.claude/rules/siblings.md`). If one half can't land, the final
message says so. Where a shared milestone has no spec yet, the session writes the spec or ADR first.

### M11: Release readiness → v0.1.0 on GitHub only (shared)
Scope: fix what blocks the first release, then cut it on GitHub only (decided 2026-11-13). No
package channel gets anything until M19. No new features.
- [x] Package channels are off in the committed config: the cask and Scoop `skip_upload: true`
  and `dockers_v2` disabled for releases.
  goreleaser still renders the cask and the manifest into `dist/`, and `release.yml` keeps them as
  a workflow artifact so each release shows what M19 would publish.
- [x] The Homebrew cask installs real completion scripts. Today `args: [completion]` plus
  `shell_parameter_format: cobra` makes brew run `tfcensus completion completion bash`, which
  prints help text. Drop `args` (M12 replaces the stanza) and check the rendered cask in `dist/`.
- [x] The container image builds once in CI before the merge: ci run 43394698104 (PR #20,
  fa580c2) built `ghcr.io/sassdavid/tfcensus` for amd64 and arm64 and ran `version` on amd64.
- [x] Paperwork from the 2026-11-13 audit: tick inventory.md criterion 5 (nightly run
  43206897470) and the config.md criterion (`TestSpec_config_1` to `_3`). Correct the release
  versions in `overview.md`, `architecture.md` and `cli.md`. Change `--fail-on audit:<severity>`
  in `cli.md` to the real `--fail-on high` syntax. Drop the dead `docs/validation/TODO.md` link
  in `testing.md`.
- [x] One `live.yml` run on main includes the move step: run 43393121759 (workflow_dispatch,
  443c526) passed `TestSpec_inventory_5` and `TestSpec_move_2`.
- [x] Release PR #2 is merged and v0.1.0 is a GitHub release. `cosign verify-blob` and
  `gh attestation verify` pass on a downloaded archive, `mise use github:sassdavid/tfcensus` works,
  and the tap and the bucket get no commit. Evidence from 2026-11-13: release run 43401178209
  published 14 assets and the `package-manifests` artifact; on `tfcensus_0.1.0_darwin_arm64.tar.gz`
  cosign printed "Verified OK", `shasum -c` "OK" and `gh attestation verify` succeeded (signer
  workflow `release.yml`); `mise use github:sassdavid/tfcensus` installed 0.1.0; both the tap and
  the bucket API answer 409 "Git Repository is empty".
- Still optional and recommended: the M6 false-positive review.

### M12: Shell completion, carapace and man pages (shared)
Scope: every flag and argument completes from local data in every common shell, and every channel
ships the scripts and man pages. bash, zsh, fish and PowerShell use cobra's scripts. nushell,
elvish, xonsh, tcsh, oil and clink use embedded carapace, so users install nothing extra.
- [x] Every enum flag completes its values with descriptions and `ShellCompDirectiveNoFileComp`:
  `--format`, `--color`, `--log-format`, `--fail-on`, `--status`, `--severity`, `--category`,
  `--mode`, `--import-by`, `--layers` and `--types`. One list feeds both the help text and the
  completion (the gh `StringEnumFlag` pattern). JSON file flags (`--index`, `--report-file`,
  `--scan`, `--audit`, `--drift`) filter by extension, `--config` by yaml, directory flags use
  `FilterDirs`.
  Evidence (2026-11-13, local): `testdata/script/complete.txtar` goldens for `--format`,
  `scan --fail-on`, `report scan --category`, `--config`, `scan --rules` and `codify --into`;
  `TestCompletionFuncs` and `TestJoinAndUsage` in `internal/complete`.
- [x] A test walks `NewRootCmd`: every non-bool flag has a completion func, a file or directory
  annotation, or an allowlist entry with a reason.
  Evidence: `TestCompletionCoverage` (`internal/cli/completion_test.go`) also checks argument
  completion; the allowlist is empty.
- [x] Dynamic completion reads local files only. `--profile` and `--management-profile` list the
  section names in `AWS_CONFIG_FILE` or `~/.aws/config` (never the credentials file, never
  `config.LoadDefaultConfig`). `--unit` lists units from the `--index` file on the command line.
  Each func has a 1 s timeout and returns `ShellCompDirectiveError` with empty stdout on failure.
  A test or depguard rule proves no AWS client, `execx` or `httpx` call is reachable, and the
  output passes the state-attribute grep test.
  Evidence: `TestProfiles`, `TestUnits`, `TestDynamic` and `TestNoNetworkReachable` (`go list -deps`)
  in `internal/complete`; `TestInventoryAttributeValues` greps the `--unit` and `--path`
  completions of the saved index.
- [x] `__complete` writes only results to stdout and does no I/O in `PersistentPreRunE`. Warm
  runs stay under 100 ms.
  Evidence: `TestCompleteSkipsSetup` (an unwritable `--log-file` doesn't fail
  `__complete`) and `TestCompleteIsFast` (best of 5 in-process runs under 100 ms with `-race`).
- [x] carapace is embedded at the same pinned version in both repos. `Gen` runs outside the
  constructors parallel tests use, and `go test -race` passes.
  `tfcensus completion nushell|elvish|xonsh|tcsh|oil|clink` prints the snippet. The owner has
  read carapace's `LICENSE.appendix`, and `lint:licenses` passes.
  Evidence: v1.18.1 in both go.mod files, `cli.Main` calls `Gen`, `mise run check` and
  `lint:licenses` pass, `complete.txtar` checks every snippet. carapace is Apache-2.0 (its
  vendored `third_party` code is MIT and BSD-2-Clause, all in `third_party_licenses/`); the
  appendix reserves the `Uid`, `Query` and `Trace` features for Tabdance, which no code here
  calls. The owner accepted it on 2026-11-13.
- [x] testscript goldens cover `__complete`, `__completeNoDesc` and `_carapace export` for static,
  enum, dynamic, missing-file and timeout cases. They live outside the shared `completion.txtar`.
  Evidence: `testdata/script/complete.txtar`; the timeout case reads a FIFO as
  `AWS_CONFIG_FILE` (`[exec:mkfifo]`, skipped on Windows).
- [ ] A CI job loads the scripts in real shells: bash 5 with bash-completion (ubuntu), bash 3.2
  with brew bash-completion v1 (macos), zsh with `compinit`, fish with `complete -C`, pwsh
  `TabExpansion2` on all three OSes, Windows PowerShell 5.1, and nushell through the carapace
  snippet.
  Open: the `completion-shells` job in `ci.yml` and `scripts/completion-shells.sh`/`.ps1` exist but
  have not run in CI yet. Locally on macOS, zsh, nushell 0.116.1 and pwsh 7.7.0 passed
  (`completion-shells.sh zsh|nushell`, `completion-shells.ps1`).
- [x] A shared `scripts/completions.sh` (on the sibling-diff "identical" list) writes
  `completions/` (bash, zsh, fish, ps1) and `manpages/*.1.gz` (`GenManTree` from
  `scripts/gendocs`, dates from `SOURCE_DATE_EPOCH`, the same bytes on a second run). Every
  archive contains both directories.
  Evidence (local): `goreleaser release --snapshot --clean --skip=sign,docker,sbom` put
  `completions/` (4 files) and 43 `manpages/*.1.gz` in all 6 archives; two runs of
  `bash scripts/completions.sh` gave the same man page checksum. The CI snapshot job checks both.
- [x] The cask uses goreleaser's `completions:` and `manpages:` keys instead of
  `generate_completions_from_executable`. Scoop's `post_install` prints the `$PROFILE` line with
  the `current` path.
  Evidence: the snapshot's `dist/homebrew/Casks/tfcensus.rb` has `bash_completion`,
  `zsh_completion`, `fish_completion` and 43 `manpage` lines; `dist/scoop/bucket/tfcensus.json`
  has the `post_install` lines.
- [x] An install page covers each shell and channel and the pitfalls in
  [roadmap-research.md](roadmap-research.md). An ADR records the shell, carapace and man page
  choices.
  Evidence: [docs/install.md](../install.md) and engineering
  [ADR-0013](../engineering/adr/0013-shell-completion-and-man-pages.md).

### M13: CI outputs and the GitHub PR comment (shared) → v0.2.0
Scope: everything the action (M14) needs from the CLI, so the action only wires flags.
- [ ] A repeatable `--out <format>=<path>` flag in the shared `internal/render`. One run writes
  json, sarif, md and github output with the same exit code as `--format`, shown by a testscript.
- [ ] A shared `internal/ghcomment` package on `internal/httpx` (no go-github; an ADR amends
  ADR-0012). `notify github-pr-comment` keeps one comment per kind per PR with the
  `<!-- tfcensus:v1 ... -->` marker. An unchanged hash means no write, and a resolved report
  becomes "Nothing to report". The PR number comes from `GITHUB_EVENT_PATH`. Cassettes cover
  create, unchanged (0 writes), update and resolve.
- [ ] Markdown truncation counts characters, defaults to 60,000 (GitHub's cap is 65,536), cuts at
  row or section boundaries and adds a footer naming what was cut. A property test checks the
  limit. The flag has the same name in both CLIs.
- [ ] `docs/ci/` uses `--out` and the PR comment, and actionlint passes.
- [ ] v0.2.0 is released.

### M14: GitHub Action → `sassdavid/tfcensus-action` v1.0.0 (clone at `~/src/github/tfcensus-action`, empty) (shared with relgap-action)
Scope: a composite action in its own repo that installs a verified release and wires M13's flags.
It never takes AWS credentials: users run `configure-aws-credentials` and terragrunt first.
Design in [roadmap-research.md](roadmap-research.md#github-action).
- [ ] `scripts/sibling-diff.sh` reads the tool name from a `.sibling` file or the directory name
  instead of `go.mod`, stays verbatim in all four repos, and each action repo has its file list.
- [ ] Skeleton: `action.yml`, `scripts/install.sh`, `scripts/run.sh` and `scripts/tool.env` (the
  only tool-specific file besides the README). Install checks sha256 and
  `gh attestation verify --signer-workflow sassdavid/tfcensus/.github/workflows/release.yml`,
  with cosign and checksum-only as options. A tampered archive fails in CI. CI runs on ubuntu,
  macos and windows, zizmor and actionlint pass, every `uses:` is pinned by SHA, and
  release-please publishes immutable releases with a moving `v1` tag.
- [ ] Report mode: one run writes json, SARIF, md and annotations. SARIF shows in code scanning on
  a test repo, and `fail-on` fails the step on exit 3.
- [ ] Comment mode: one sticky comment on a test repo's terragrunt PR. A re-run edits it with no
  duplicate, and a fork PR falls back to the step summary.
- [ ] v1.0.0 is on the Marketplace under a name that doesn't collide, and `docs/ci/drift.yml`
  uses the action.

### M15: MCP prompts and the LLM decision (shared, different per repo) → part of v0.3.0
Scope: relgap gets an opt-in `--summarize`. tfcensus doesn't, by decision on 2026-11-13: its
findings are already exact, the useful questions need attribute values it must not show, and its
identifiers map the organization. Trivy, Snyk, Steampipe and Infracost use the same split.
- [ ] MCP prompt support in the shared `internal/mcpkit`. tfcensus ships `triage_drift` and
  `explain_unmanaged`. They tell the agent to cite rule IDs and addresses and never to ask for
  attribute values. The mcptest lists and fetches them, and the grep test covers the prompt text.
- [ ] An ADR records "no built-in LLM call, agents use MCP", and `.claude/rules/siblings.md` lists
  it as an intended difference.
- [ ] `docs/ci/ai-summary.md` shows an opt-in workflow step that sends
  `tfcensus report --format json` to a model the organization picks (Bedrock in its own account or
  `actions/ai-inference`), and lists which identifiers leave the runner. actionlint passes.

### M16: Planned CLI gaps (shared where both repos lack it) → v0.3.0
- [ ] `config init` writes a commented `.tfcensus.yaml` and refuses to overwrite without
  `--force`. The same shape as relgap's.
- [ ] `cache info|clear` for the probe and scan caches (plan ADR-0009), modeled on relgap's
  `cache` command.
- [ ] `doctor` reports the terragrunt and terraform or tofu versions and fails below the minimums
  in `docs/engineering/version-policy.md` (risk R5).
- [ ] `--account`, `--exclude-account` and `--all-regions` from `cli.md`.
- [ ] The config search stops at the git root, through an option in the shared
  `internal/config/load.go`.

### M17: Scan coverage → v0.4.0 (tfcensus only)
- [ ] The Config aggregator fast path in `internal/inventory` (inventory.md, risk R3, open question
  Q3), with the probe result from `doctor`.
- [ ] CloudFormation as a second IaC source: stack resources become `managed-by-cfn` with the
  stack name (open question Q4).

### M18: Move and drift depth → v0.4.0 (tfcensus only)
- [ ] `move suggest` drafts a migration file from plans (move.md, tfautomv engine).
- [ ] `move` supports non-default workspaces of s3 backends (move.md).
- [ ] OpenTofu encrypted state is read through `tofu show -json` (risk R6).
- [ ] An L3 drift e2e runs real terraform and terragrunt against moto, and a nightly terragrunt
  contract test runs in `live.yml` (testing.md, risk R5).

### M19: Package channels (shared, the owner picks the release)
Scope: from the next release on, publish to Homebrew, Scoop and ghcr.io as well as GitHub.
Earlier versions stay GitHub only; the tap and the bucket start at that version.
- [ ] The tap and bucket tokens use `{{ .Env.HOMEBREW_TAP_TOKEN }}` and
  `{{ .Env.SCOOP_BUCKET_TOKEN }}`. goreleaser renders `repository.token` with
  `ApplySingleEnvOnly` (`internal/client/client.go`), so the current `envOrDefault` form fails on
  the first real publish. Skipped runs never read the token, which is why nothing caught it.
- [ ] `sassdavid/homebrew-tap` and `sassdavid/scoop-bucket` have a first commit on their default
  branch, and the owner confirms both secrets exist.
- [ ] `skip_upload` becomes
  `'{{ if and (isEnvSet "HOMEBREW_TAP_TOKEN") (not .Prerelease) }}false{{ else }}true{{ end }}'`
  (and the Scoop equivalent), in one `build:` commit that release-please lists in the changelog.
  A later `-rc` tag, if one is ever cut, stays GitHub only, like goreleaser's `skip_upload: auto`.
- [ ] The cask from the last GitHub-only release's artifact installs from a local test tap
  (`brew tap-new`) before the switch, with completions and man pages in brew's directories.
- [ ] The ghcr image is enabled, the package is public, and
  `docker run ghcr.io/sassdavid/tfcensus version` prints the release.
- [ ] After the next release, `brew install --cask sassdavid/tap/tfcensus` and
  `scoop install tfcensus` install it on a clean machine or runner.

### M20: Package registries (shared)
- [ ] An aqua-registry PR for `sassdavid/tfcensus` is merged (any time after v0.1.0).
- [ ] A mise registry PR is merged, once there are 3 or more external users. mise's `packslip:`
  backend, which carries completions and man pages, is checked again for goreleaser support.
