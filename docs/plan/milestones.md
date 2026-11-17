# tfcensus: milestones

**One milestone = one fresh Claude Code session.** Start each session with:
"Read `CLAUDE.md`, then `docs/plan/milestones.md` M<n> and the linked specs. Implement M<n> only."
Tick a box only when its check has passed, and quote the evidence in the commit body.
Never run anything against real AWS accounts in a session unless the prompt explicitly allows a named read-only profile.

Release mapping (revised 2026-11-13): every release goes to GitHub only (archives, checksums,
SBOMs, the cosign bundle and attestations) until M19 turns the package channels on.
**v0.1.0 = M0-M11**, v0.2.0 = M12-M13, v0.3.0 = M15-M18, and M14 releases the separate action
repo (revised 2026-11-16: M17-M18 ship in v0.3.0, and v0.4.0 = M21). M19 has no fixed version or
place in the order: the owner runs it when the GitHub releases look right. M20 waits for external
users. M21 (scan accuracy) comes from the first real-account scan and should ship before
tfcensus is announced. M22-M26 (docs site, added 2026-11-17) have no version: the site deploys
with each release once M22 is done, and M22, M23 and M26 run in the same session as relgap's
milestones of the same number. The research and decisions behind M11-M20 are in
[roadmap-research.md](roadmap-research.md).

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
- [x] *(Optional, manual, strongly recommended before announcing v0.1)* A false-positive review on one real non-prod account, under 5% (recorded, aggregated only). Done at v0.4.0 on three accounts (M21, `docs/validation/nonprod-scan.md`).
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
- The M6 false-positive review is done at v0.4.0 (M21).

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
- [x] A CI job loads the scripts in real shells: bash 5 with bash-completion (ubuntu), bash 3.2
  with brew bash-completion v1 (macos), zsh with `compinit`, fish with `complete -C`, pwsh
  `TabExpansion2` on all three OSes, Windows PowerShell 5.1, and nushell through the carapace
  snippet.
  Evidence (2026-11-13): the `completion-shells` jobs of ci run 43433805686 on main (after #23)
  passed on ubuntu, macos and windows. The logs print "bash: ok" for bash 5 and bash 3.2, "zsh: ok",
  "fish: ok", "nushell: ok", and "powershell: ok" for pwsh 7.6 on each OS and for Windows
  PowerShell 5.1.26100.
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
Decisions: [engineering ADR-0014](../engineering/adr/0014-report-outputs-and-pr-comment.md).
- [x] A repeatable `--out <format>=<path>` flag in the shared `internal/render`. One run writes
  json, sarif, md and github output with the same exit code as `--format`, shown by a testscript.
  Evidence (2026-11-13, local): `testdata/script/out.txtar` runs drift once with `--format json`
  and four `--out` files, exits 3 under `--fail-on drift`, and each file equals the single-format
  run; `TestParseOutputs` and `TestSinkWrite` in `internal/render`. `units`, `inventory`,
  `audit backend`, `scan`, `drift` and `whois` take `--out`; `scan` and `audit backend` gain
  `--format github`.
- [x] A shared `internal/ghcomment` package on `internal/httpx` (no go-github; an ADR amends
  ADR-0012). `notify github-pr-comment` keeps one comment per kind per PR with the
  `<!-- tfcensus:v1 ... -->` marker. An unchanged hash means no write, and a resolved report
  becomes "Nothing to report". The PR number comes from `GITHUB_EVENT_PATH`. Cassettes cover
  create, unchanged (0 writes), update and resolve.
  Evidence: `TestPublish` replays `testdata/cassettes/github/pr-comment-{create,unchanged,update,resolve,skip}.yaml`
  and counts writes (1, 0, 1, 1, 0; the dry run 0); `TestFromEnv` reads `pull_request` and
  `issue_comment` events; `TestGitHubPRLifecycle` in `internal/notify`. Engineering ADR-0014
  amends plan ADR-0012.
- [x] Markdown truncation counts characters, defaults to 60,000 (GitHub's cap is 65,536), cuts at
  row or section boundaries and adds a footer naming what was cut. A property test checks the
  limit. The flag has the same name in both CLIs.
  Evidence: `--md-max-chars` in both CLIs; `TestTruncateMarkdownLimit` (400 generated reports,
  limits at, below and above the length) and `TestTruncateMarkdownFooter`; `TestSpec_drift_4` on
  300 units.
- [x] `docs/ci/` uses `--out` and the PR comment, and actionlint passes.
  Evidence: `drift.yml` runs tfcensus once with `--out sarif=` and `--out md=`; `drift-pr.yml`
  runs `notify --notify github-pr-comment`; `jactionlint docs/ci/drift.yml docs/ci/drift-pr.yml`
  exits 0.
- [x] v0.2.0 is released.
  Evidence (2026-11-13): release PR #24 merged; release run 43448136324 published v0.2.0 (tag on
  cb5d0d2) with 14 assets and the `package-manifests` artifact. On
  `tfcensus_0.2.0_darwin_arm64.tar.gz` cosign printed "Verified OK", `shasum -c` "OK" and
  `gh attestation verify --signer-workflow sassdavid/tfcensus/.github/workflows/release.yml`
  exited 0; `mise use github:sassdavid/tfcensus@0.2.0` installed it and `tfcensus version` prints
  0.2.0. The tap and the bucket API still answer 409 "Git Repository is empty".

### M14: GitHub Action → `sassdavid/tfcensus-action` v1.0.0 (clone at `~/src/github/tfcensus-action`, empty) (shared with relgap-action)
Scope: a composite action in its own repo that installs a verified release and wires M13's flags.
It never takes AWS credentials: users run `configure-aws-credentials` and terragrunt first.
Design in [roadmap-research.md](roadmap-research.md#github-action).
- [x] `scripts/sibling-diff.sh` reads the tool name from a `.sibling` file or the directory name
  instead of `go.mod`, stays verbatim in all four repos, and each action repo has its file list.
  Evidence (2026-11-15, local): `mise run sibling:diff` prints "shared files match" in relgap,
  tfcensus, relgap-action and tfcensus-action. An action repo also compares the verbatim files
  and `writing.md` with its tool's repo. A copy in a directory with another name exits 2 and
  passes with `.sibling`. `.release-please-manifest.json` moved to the similar list, since the
  two tools release different versions.
- [x] Skeleton: `action.yml`, `scripts/install.sh`, `scripts/run.sh` and `scripts/tool.env` (the
  only tool-specific file besides the README). Install checks sha256 and
  `gh attestation verify --signer-workflow sassdavid/tfcensus/.github/workflows/release.yml`,
  with cosign and checksum-only as options. A tampered archive fails in CI. CI runs on ubuntu,
  macos and windows, zizmor and actionlint pass, every `uses:` is pinned by SHA, and
  release-please publishes immutable releases with a moving `v1` tag.
  Evidence (2026-11-15, tfcensus-action v1.0.0 = feb168b): `ci` run 43727583429 passed `lint`
  (jactionlint, shellcheck, prose), `test` on ubuntu, macos and windows (`scripts/test-install.sh`
  fails a tampered archive in every verify mode) and the action job for attestation, cosign and
  checksum on all three. `security` run 43727583387 passed zizmor. Every `uses:` is a SHA or `$/`.
  release-please drafted v1.0.0 from #2. After it was published, the API reports
  `"immutable": true`, `gh release verify v1.0.0` prints "Release v1.0.0 verified!", and
  `major-tag` run 43728339335 moved `v1` to feb168b, the commit of `v1.0.0`.
- [x] Report mode: one run writes json, SARIF, md and annotations. SARIF shows in code scanning on
  a test repo, and `fail-on` fails the step on exit 3.
  Evidence (2026-11-15, sassdavid/action-test, tfcensus-action#3): `drift --from-json-out-dir`
  on recorded plans (`testdata/drift/ingest` without its failed unit, no AWS). Push run
  43722958899 uploaded `tfcensus-drift`, and code scanning lists 8 open `drift` alerts on the
  units' `terragrunt.hcl` files. The macOS and Windows jobs wrote all three files. The dispatched
  `gate` job (run 43723754174) printed `--fail-on matched (drift: 6)` and failed with exit
  code 3.
- [x] Comment mode: one sticky comment on a test repo's terragrunt PR. A re-run edits it with no
  duplicate, and a fork PR falls back to the step summary.
  Evidence (2026-11-15, sassdavid/action-test#2): one comment from github-actions[bot]. A re-run
  of 43723751251 logged `unchanged` and added none, and later commits that changed the noise
  presets edited the same comment (new hash). `scripts/test-run.sh` runs the fork and
  Dependabot fallback with a fake event on ubuntu, macos and windows.
- [x] v1.0.0 is on the Marketplace under a name that doesn't collide, and `docs/ci/drift.yml`
  uses the action.
  Evidence (2026-11-15): published at 15:09 UTC as
  https://github.com/marketplace/actions/tfcensus (HTTP 200). Before that the page answered 404,
  and GitHub has no user or org named tfcensus. v1.0.0 missed `<from>/report.json` when `from` is
  absolute. tfcensus-action#5 fixed it, and v1.0.1 (f2cc1dc, published 15:22 UTC) is immutable,
  passed `ci`, `gh release verify` and `major-tag` run 43729541373, and `v1` points at it.
  `docs/ci/drift.yml` runs `sassdavid/tfcensus-action@f2cc1dc # v1.0.1` in report mode with
  `fail-on: drift,errors`, and jactionlint passes on it.

### M15: MCP prompts and the LLM decision (shared, different per repo) → part of v0.3.0
Scope: relgap gets an opt-in `--summarize`. tfcensus doesn't, by decision on 2026-11-13: its
findings are already exact, the useful questions need attribute values it must not show, and its
identifiers map the organization. Trivy, Snyk, Steampipe and Infracost use the same split.
- [x] MCP prompt support in the shared `internal/mcpkit`. tfcensus ships `triage_drift` and
  `explain_unmanaged`. They tell the agent to cite rule IDs and addresses and never to ask for
  attribute values. The mcptest lists and fetches them, and the grep test covers the prompt text.
  Evidence: `TestPrompt_list`, `TestPrompt_get`, `TestPrompt_errors` and
  `TestAddPrompt_duplicateArgument` (`internal/mcpkit/prompt_test.go`, identical in relgap);
  `TestMCPPrompts` (`internal/cli/mcp_test.go`) lists and fetches both prompts, greps their text
  for the `driftValues()` fixture values and checks that every tool they name exists.
- [x] An ADR records "no built-in LLM call, agents use MCP", and `.claude/rules/siblings.md` lists
  it as an intended difference.
  Evidence: [ADR-0013](adr/0013-no-built-in-llm.md); siblings.md "Intended differences", identical
  in all four repos (`mise run sibling:diff`).
- [x] `docs/ci/ai-summary.md` shows an opt-in workflow step that sends
  `tfcensus report --format json` to a model the organization picks (Bedrock in its own account or
  `actions/ai-inference`), and lists which identifiers leave the runner. actionlint passes.
  Evidence: [docs/ci/ai-summary.yml](../ci/ai-summary.yml) sends `tfcensus report drift
  --format json` to Bedrock or GitHub Copilot when `AI_SUMMARY` is set;
  `jactionlint docs/ci/ai-summary.yml docs/ci/drift.yml docs/ci/drift-pr.yml` exits 0. The
  Copilot job calls the Copilot CLI instead of `actions/ai-inference` (ADR-0013 says why).

### M16: Planned CLI gaps (shared where both repos lack it) → v0.3.0
- [x] `config init` writes a commented `.tfcensus.yaml` and refuses to overwrite without
  `--force`. The same shape as relgap's.
- [x] `cache info|clear` for the probe and scan caches (plan ADR-0009), modeled on relgap's
  `cache` command. One row per cache; `clear` deletes both (`testdata/script/cache.txtar`).
- [x] `doctor` reports the terragrunt and terraform or tofu versions and fails below the minimums
  in `docs/engineering/version-policy.md` (risk R5). A missing binary is reported, not a failure
  (`internal/toolcheck`, `TestDoctorToolVersions`).
- [x] `--account`, `--exclude-account` and `--all-regions` from `cli.md`. The account filters are on
  `doctor`, `scan` and `whois`; `--all-regions` (`account:ListRegions`) is on `doctor` and `scan`,
  because `whois` lists only the answer's own region (`testdata/script/targets.txtar`).
- [x] The config search stops at the git root, through an option in the shared
  `internal/config/load.go` (`config.Options.StopDir`). Outside a repository only the working
  directory counts.

### M17: Scan coverage → v0.3.0 (tfcensus only)
- [x] The Config aggregator fast path in `internal/inventory` (inventory.md, risk R3, open question
  Q3), with the probe result from `doctor`. Inventory criterion 6 passes on cassettes; Q3 stays
  open until a read-only run against the real org.
- [x] CloudFormation as a second IaC source: stack resources become `managed-by-cfn` with the
  stack name (open question Q4). Done as `other-iac` with rule `source/cloudformation`, the stack
  on the finding, and `scan.cloudformation` on by default ([ADR-0014](adr/0014-cloudformation-stack-source.md),
  scan.md criterion 5). Evidence: `mise run check` exit 0 on cassettes, `mise run sibling:diff`
  matches. Q4 resolved.
- Not in M17: whois and codify still read only the CloudFormation tags (ADR-0014 follow-up).

### M18: Move and drift depth → v0.3.0 (tfcensus only)
- [x] `move suggest` drafts a migration file from plans (move.md, own strict 1:1 engine,
  [ADR-0016](adr/0016-own-move-suggest-engine.md)).
- [x] `move` supports non-default workspaces of s3 backends (move.md).
- [x] OpenTofu encrypted state is read through `tofu show -json` (risk R6, [ADR-0015](adr/0015-read-encrypted-state-with-tofu.md)).
- [x] An L3 drift e2e runs real terraform and terragrunt against moto, and a nightly terragrunt
  contract test runs in `live.yml` (testing.md, risk R5).

### M19: Package channels (shared, the owner picks the release)
Scope: from the next release on, publish to Homebrew, Scoop and ghcr.io as well as GitHub.
Earlier versions stay GitHub only; the tap and the bucket start at that version.
- [x] The tap and bucket tokens use `{{ .Env.HOMEBREW_TAP_TOKEN }}` and
  `{{ .Env.SCOOP_BUCKET_TOKEN }}`. goreleaser renders `repository.token` with
  `ApplySingleEnvOnly` (`internal/client/client.go`), so the old `envOrDefault` form would have failed on
  the first real publish. Skipped runs never read the token, which is why nothing caught it.
  Evidence (2026-11-16, goreleaser 2.20.2): `envOnlyRe` in `internal/tmpl/tmpl.go` accepts only
  `{{ .Env.NAME }}`, and the cask and Scoop `doPublish` return on `skip_upload: true` before
  `client.NewIfToken` reads the token. With both variables unset, `goreleaser check` validates
  the file and `goreleaser release --snapshot --clean --skip=sign,sbom` renders the cask and the
  manifest.
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

### M21: Scan accuracy on real accounts → v0.4.0 (tfcensus only) ([scan-accuracy](specs/scan-accuracy.md))
Scope: fix everything the first real-account scan got wrong, in one release, and measure it.
The v0.3.0 scan of one non-prod account gave 147 false `unmanaged` findings out of 171 (86%),
650 `undetermined` findings that were all "no type mapping", and missed VPC Lattice resources and
ACM certificates. Decisions: [ADR-0017](adr/0017-scan-accuracy.md) (accepted 2026-11-16).
The read-only rerun in the last box needs a prompt that names the profile, as the header says.
If one session can't finish, the open boxes carry over to the next session, and v0.4.0 waits for
the last box.
- [x] Inheritance: a second classification pass gives `unmanaged` and `undetermined` children
  the category of an `aws-managed`, `other-iac` or `derived` parent (`inherit/<category>`), keeps
  children of `managed` parents `unmanaged`, honours `never_hide`, and `--explain` prints the
  parent chain. New parent links for the regional NAT gateway route table and the `CHILD`
  resource configuration. (scan-accuracy criterion 1)
- [x] ENIs: `service-eni` replaces `requester-managed-eni`, and the config query selects the ENI
  `description`, `requesterId` and `attachment.instanceOwnerId`, plus `dhcpConfigurations` and
  route table `associations`. inventory.md and the comment in `config.go` follow ADR-0017.
  (criterion 2)
- [x] Rules: the Kubernetes controller rules (external-dns with prefix and suffix settings, AWS
  Load Balancer Controller and in-tree rules, VPC CNI, EBS and EFS CSI, ACK, the EKS cluster
  security group), the AWS default rules (apex NS and SOA, default DHCP options, main route
  table, regional NAT route table, account defaults checked on the validation account) and the
  AWS service rules (Control Tower widened, AFT, QuickSight, API Gateway log groups,
  service-linked secrets, log-delivery bucket policies, Lambda versions, RDS automated snapshots,
  Backup recovery points, add-on Pod Identity associations), each with positive and negative
  fixtures. ownership-rules.md's pack table lists them. (criterion 3)
- [x] Matching: `aws_iam_policy_attachment` expands to one key per principal, and the
  `aws_default_*` types are curated. (criterion 4)
- [x] Typemap: every type in scan-accuracy.md's table is curated, or its entry says why it can't
  be listed. A test keeps the list curated, `guessCFN` has the alias table, and Config and
  CloudFormation type names that differ (`AWS::OpenSearch::Domain` and
  `AWS::OpenSearchService::Domain`) map to one entry. matching.md's "about 300 types" line is
  updated with the real count. (criterion 5)
- [x] Resource Explorer: records get an `id`, RE-only records of a Config-listed type are dropped
  and counted, and the Kubernetes object types are skipped and counted. (criterion 6)
- [x] Cloud Control: `cc: true` types (the VPC Lattice resource gateway, resource configuration
  and service network associations) are listed by default, and the resource configuration type
  is read. (criterion 7)
- [x] `--explain-coverage` lists the AWS Config types scan doesn't map, with counts per account
  and region. (criterion 8)
- [x] `testdata/validation/` holds an anonymized fixture for each of the 13 false-positive
  groups, and a testscript scans them with no `unmanaged` finding. `TestSpec_accounts_4` passes,
  so the read-only policy needs no new action. `mise run validation:stats` prints a reviewed CSV's
  aggregate row. (criterion 9)
- [x] The read-only rerun on validation account 1 with the v0.3.0 command: under 5% false
  positives among `unmanaged` findings, no `undetermined` finding of a curated type, and the VPC
  Lattice resources and ACM certificates are listed. `docs/validation/nonprod-scan.md` has the
  before and after rows (aggregates only), and ownership-rules.md criterion 5 and the M6 optional
  box are ticked. (criterion 10)
- [x] Every further account the owner supplies gets a row in `docs/validation/nonprod-scan.md`.
  Its false-positive groups get a rule and a fixture in this milestone, or an issue when the fix
  needs a design of its own.
- Not in M21: the Config aggregator (open question Q3), the external-dns DynamoDB and AWS Cloud
  Map registries, EKS Auto Mode resources that EC2 hides from Describe calls, whois and codify
  reading the new rules (they share the engine, but their goldens are a follow-up).

### M22: Docs site foundation (shared) ([docs-site](specs/docs-site.md), [docs-design](specs/docs-design.md))
Scope: the site builds in CI, deploys on release to `https://tfcensus.sasskovacs.dev`, has its
theme, fonts and home page, and serves the existing user docs. The CLI reference stays excluded
until M23. Decisions: engineering [ADR-0016](../engineering/adr/0016-docs-site.md) (proposed
2026-11-17; this milestone accepts it in both repos). relgap does the same milestone with its own
theme in the same session.
- [x] ADR-0016 is accepted in both repos and listed in `docs/engineering/README.md`.
- [x] `mise.docs.toml` pins node 26.13.1, aube 2.10.1, lychee and oxfmt, `mise.lock` has their
  entries, and `MISE_ENV=docs mise install` works on a clean checkout. The entries are in
  `mise.docs.lock`, as `mise.e2e.lock` is for the e2e tools, and oxfmt's npm lockfile is under
  `.mise/locks/`.
- [x] `docs/package.json` (exact versions), `docs/aube-lock.yaml` and `docs/aube-workspace.yaml`
  exist. `aube install --frozen-lockfile` passes with lifecycle scripts denied, or each allowed
  package is listed with a reason.
- [x] `docs/.vitepress/config.mts` follows docs-site.md "Site configuration": cleanUrls,
  lastUpdated, sitemap, local search, editLink, srcExclude (including `cli/**` for now), the
  version nav item from `.release-please-manifest.json`, and the `description` check in
  `transformPageData`.
- [x] `theme/tokens.css` has every token in docs-design.md for both themes, fonts load from the
  Fontsource packages, and the contrast check passes (docs-design criterion 1).
- [x] `HomeHero`, `CensusTally`, `CategoryMark`, `AnswerRows` and `SiteFooter` exist. The tally
  reads `home-tally.json`, written by a generator from a scan golden. The home page matches the
  wireframe at 1280 px and 400 px (screenshots in the PR).
- [x] The existing user pages (install, CI, IAM, runbooks) build and have a `description`. The
  build has no dead links.
- [x] mise tasks `docs:dev`, `docs:build`, `docs:preview` and `lint:docs` exist. `docs:build` runs
  the checks in docs-site.md "Checks" that exist at this point (stale check, unit tests, build,
  lychee). `mise run check` includes `lint:docs`.
- [x] `.github/workflows/docs.yml` builds on pull requests and deploys through `workflow_call`
  from `release-please.yml` and through `workflow_dispatch`. jactionlint passes. A dispatch run
  from the latest tag deployed the site: `curl -sI https://tfcensus.sasskovacs.dev/` shows 200 and
  the `http://` URL redirects. "Enforce HTTPS" is on.
  The dispatch ran from `main` (run 44052711619), because v0.4.0 predates the site; the spec allows
  `ref: main` for a manual deploy.
- [ ] `.github/workflows/aube-lock.yml` and the `aube-lock` environment exist. A test Renovate
  branch that bumps a docs dependency gets a lockfile commit and green CI. `renovate.json` covers
  `docs/package.json`, and `docs/ci/` stays out of scope as before.
  The workflow, the environment (branch rule, secret, variable) and the `renovate.json` rule are in
  place; the test Renovate branch is still to come.
- [x] `lint:prose` covers the published pages, with a test that it doesn't skip them.
- [x] `scripts/sibling-diff.sh` lists the new files as docs-site.md "Sibling sharing" says, and
  `mise run sibling:diff` passes in both repos.

### M23: Generated reference (shared) ([docs-site](specs/docs-site.md#generated-content-shared-generators))
Scope: every fact the code can state reaches the site from code, with a test per page. The
generators are shared with relgap; tool-specific catalogues wait for M24.
- [x] `scripts/gendocs` writes the nested CLI reference with frontmatter, flag tables, formats,
  `--fail-on` values, exit codes, examples, subcommands and related links; one global flags page;
  one completion page; escaped `Long` text with indented blocks as fences; `commands.json`. The
  flat `tfcensus_*.md` files are gone, links to them are updated, and `cli/**` leaves
  `srcExclude`. (docs-site criterion 4)
- [x] `Annotations["formats"]`, `["fail-on"]` and `["exit-codes"]` are set on every command, with
  tests that run the commands and compare. The stale help texts in docs-content.md (inventory,
  move and notify exit codes, `config` env rule, `whois --live` default) are fixed.
  Every runnable command has the annotations; group commands run nothing. The root checks
  `--format`, `--out` and `--fail-on` against them before the command runs. The exit-code test
  reads the code behind each RunE (`TestAnnotations_exitCodes`); `mcp` is the one exception, with
  its reason in the test.
- [x] A test fails for a non-hidden command without `Long` or `Example` outside the allowlist,
  and the missing texts listed in docs-content.md are written.
- [x] `/reference/config`, `/reference/environment`, `/reference/exit-codes`,
  `/reference/schemas` and `/reference/mcp` are generated, each with the test docs-site.md names.
  Schemas are served at `/schemas/<name>.json` and their `$id` matches. (docs-site criterion 5)
  The tests: `TestConfigSchema`, `TestEnvironment`, `TestAnnotations_exitCodes`, `TestSchemaIDs`
  and the MCP page in `TestGenerated`. Every schema's `$id` moved to the site URL.
  `/reference/fail-on` is generated here too, because the generator is shared.
- [x] `internal/docsurl` exists, and help texts, runtime messages and the config template use it.
  A test checks every path against the built site. (docs-site criterion 6)
  `docs:build` runs `TestPaths` in `internal/docsurl` with `DOCS_DIST` set, which also checks anchors.
- [x] The snippet check runs in `docs:build` and finds nothing. (docs-site criterion 7)
- [x] `sidebar.mts` builds the CLI section from `commands.json`, and `llms.txt` is generated and
  stale-checked.
- [x] `mise run docs` regenerates everything and exits 1 on any change. A PR that changes a flag
  without regenerating fails CI. (docs-site criterion 3)

### M24: Getting started and tool reference (tfcensus only) ([docs-content](specs/docs-content.md))
Scope: the tutorial, install, supported versions, safety model and every tfcensus-specific
reference page. Rerun the audit for the pages this milestone writes before writing them.
- [ ] `scripts/gensite` generates rule packs, CEL fields, audit checks, drift noise presets, rule
  IDs, inventory layers, type mapping coverage (data JSON plus a filterable table component) and
  supported versions, each with a stale check in `mise run docs`.
- [ ] `/reference/iam` embeds the policy JSON. `TestSpec_accounts_4` gets the reverse check, and
  `kms:ListAliases` and `elasticloadbalancing:Describe*` are dropped or kept with a reason.
- [ ] Getting started runs doctor, units, inventory and scan against the fixtures, with every
  output included from goldens. Install, supported versions, `/concepts/safety` and the
  changelog page are written.
- [ ] `WritesState` appears on the three writing commands through `Annotations["docs.writes"]`.
- [ ] The never-on-the-site checks pass. (docs-content criterion 4)

### M25: Guides and concepts (tfcensus only) ([docs-content](specs/docs-content.md))
Scope: every guide, concept, help and project page in docs-content.md, and every gap closed.
If one session can't finish, the open pages carry over to a follow-up session before M26.
- [ ] The runbooks, CI examples and AI summary are ported, and the old files are removed or
  reduced to a link to the site.
- [ ] Every guide and concept page in docs-content.md exists with a `description` and passes the
  snippet check. (docs-content criteria 1 and 2)
- [ ] Every row in "wrong or stale statements" is fixed, each with its commit in the evidence.
  (docs-content criterion 3)
- [ ] README is reduced to the introduction, install and links. (docs-content criterion 5)
- [ ] Troubleshooting, FAQ, scan accuracy (aggregates only), security and contributing pages
  exist, and relgap's `drift --from-tfcensus` guide and this site link to each other.

### M26: Polish and launch (shared) ([docs-site](specs/docs-site.md), [docs-design](specs/docs-design.md))
Scope: social cards, icons, accessibility and performance, then the first deploy by a real
release.
- [ ] `social.mjs` and `social-check.mjs` generate and check a card per page with the bundled
  font and its license file. The glyph, favicons and manifest are in place. (docs-design
  criterion 4, docs-site criterion 8)
- [ ] axe shows no WCAG 2.2 AA violation on the pages docs-site.md lists in both themes, and
  Lighthouse gives accessibility 100 and best practices at least 95 on the home page and a CLI
  page. (docs-site criterion 9, docs-design criterion 5)
- [ ] The built site makes no third-party request. (docs-site criterion 10)
- [ ] `docs/README.md` is the contributor guide for the site: tasks, where pages go, generated
  files, voice.
- [x] A release created by release-please deployed the site through `workflow_call`, with the run
  ID in the evidence. (docs-site criterion 2) v0.5.0, release-please run 44053630046: the docs build
  and deploy jobs passed, and the site answers 200.
- [ ] `mise run sibling:diff` passes in both repos. (docs-site criterion 11)
