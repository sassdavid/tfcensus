# Roadmap research (2026-11-13)

This note records the research behind milestones M11 to M19 in tfcensus and relgap. Both repos
carry the same copy. Read it before starting one of those milestones, then write the spec or ADR
the milestone asks for.

## Decisions taken

| Topic | Decision |
|---|---|
| Milestone numbers | From M11 on, both repos use the same numbers. A shared milestone lands in both repos in one session. |
| Releases | Normal semver releases go to GitHub only from v0.1.0 on. M19 turns on Homebrew, Scoop and ghcr.io from a release the owner picks. No prerelease phase. |
| Cask completion bug | Fixed before v0.1.0 (M11). |
| Shells | bash, zsh, fish and PowerShell from cobra. nushell, elvish, xonsh, tcsh, oil and clink from embedded carapace, so users install nothing extra. |
| Man pages | `cobra/doc` `GenManTree`, written at release time, not linked into the binary. |
| GitHub Action | One composite action per tool in its own repo (`sassdavid/relgap-action`, `sassdavid/tfcensus-action`), released after the CLI features it needs. |
| Action repo sync | One generalized `scripts/sibling-diff.sh`, verbatim in all four repos. |
| Multi-output | A repeatable `--out <format>=<path>` flag in the shared `internal/render`. |
| LLM | relgap keeps an opt-in `--summarize`. tfcensus has no built-in LLM call and uses MCP prompts plus a CI recipe instead. Both get MCP prompts. |

## Open state found by the audit

Neither repo has a tag or a release. Each has a release-please PR #2 for 0.1.0 that is open and
contains M0 to M10, so the old per-feature release versions in the plan docs are stale.

Both `.goreleaser.yaml` files set `generate_completions_from_executable` with `args: [completion]`
and `shell_parameter_format: cobra`. Homebrew's `cobra` format already adds `completion <shell>`,
so brew runs `<tool> completion completion bash`, which exits 0 and prints help text. Sources:
https://docs.brew.sh/Cask-Cookbook, goreleaser issue #7044.

`sassdavid/homebrew-tap` and `sassdavid/scoop-bucket` are empty repos. `skip_upload` skips the cask
and the manifest silently when a token is missing.

tfcensus open items: inventory criterion 5 and the config criteria are met but unticked; `cli.md`
shows `--fail-on audit:<severity>` while the code takes `--fail-on high`; `testing.md:33` links a
missing file; the move step in `live.yml` has never run nightly; the container image has never been
built (CI snapshots skip docker).

relgap open items: the M3 precision gate passes at exactly 0.900 (36 of 40) on labels a person
hasn't reviewed; `live.yml` has never run; 28 of the 30 corpus tools are digested; several spec
boxes are unticked although their `TestSpec_` tests pass; the M12 `--binaries` item is already done.

## Release channels

Every release is a normal semver GitHub release until M19. The committed `.goreleaser.yaml` sets
`skip_upload: true` on the cask and the Scoop manifest (and disables the tfcensus image).
goreleaser still writes both files to `dist/`, so each release can keep them as a workflow
artifact. M19 flips the switch, and the tap and the bucket start at that version.

Prereleases (`v0.1.0-rc.N`) were considered and rejected for this phase:

- GitHub's `/releases/latest` never returns a prerelease, and the action's `version: latest` uses
  it.
- mise's `github:` and `aqua:` backends leave prereleases out of `latest` and version prefixes
  unless the user sets `prerelease = true` (`_reference/jdx/mise/docs/dev-tools/backends/github.md`,
  https://github.com/jdx/mise/pull/9329).
- A 0.x version already says the API can change.
- Prereleases suit a short test of one upcoming version. goreleaser's `skip_upload: auto` exists
  to keep such tags out of a tap.

goreleaser renders `repository.token` with `ApplySingleEnvOnly`
(`_reference/goreleaser/goreleaser/internal/client/client.go:172`), which accepts only
`{{ .Env.NAME }}`. Both repos use `{{ envOrDefault ... }}` there today. Skipped runs never read the
token, so the first real publish would fail. M19 fixes it. The same pattern shows up in
https://github.com/ModelsLab/modelslab-cli/pull/11 and https://github.com/getlantern/lantern-box/pull/425.

## Shell completion

Current state in both repos: cobra v1.10.2 default `completion bash|zsh|fish|powershell`, no
`ValidArgsFunction` or `RegisterFlagCompletionFunc`, so enum flags fall back to file names.
Archives ship no completion files.

Cobra facts that shape the work (`completions.go`, `site/content/completions/_index.md`):

- The hidden `__complete` command runs the root `PersistentPreRunE` with unparsed flags. Keep I/O
  out of it. Completion funcs can read already-parsed flags with `cmd.Flags()`.
- Only results may go to stdout. Debug output goes through `cobra.CompDebugln` and
  `BASH_COMP_DEBUG_FILE`.
- fish and PowerShell ignore `ShellCompDirectiveFilterFileExt` and `FilterDirs`.
- Active Help works in bash v2 and zsh only. `NoSpace` and `KeepOrder` need bash 4.4.
- cobra has no nushell support (spf13/cobra#2364 is open).

Warm `__complete` takes about 10 ms. The first run of a fresh binary on macOS took 580 ms.

Per shell:

| Shell | Homebrew cask target | Manual install | Pitfalls | CI check |
|---|---|---|---|---|
| bash | `etc/bash_completion.d/<tool>` | `~/.local/share/bash-completion/completions/<tool>` | needs the bash-completion package; macOS `/bin/bash` 3.2 needs brew `bash-completion` v1, not `@2` | source the script after bash-completion, then `complete -p <tool>` |
| zsh | `share/zsh/site-functions/_<tool>` | `_<tool>` in a `$fpath` dir before `compinit` | "command not found: compdef" means it loaded before `compinit`; stale `~/.zcompdump*`; compaudit and group-writable dirs | `zsh -f`, `compinit -u`, check `$_comps[<tool>]` |
| fish | `share/fish/vendor_completions.d/<tool>.fish` | `~/.config/fish/completions/<tool>.fish` | `completion fish \| source` in config.fish slows startup | `fish --no-config -c 'source ...; complete -C"<tool> --format "'` |
| PowerShell | not offered by goreleaser casks | dot-source the script from `$PROFILE`; Scoop path uses `current` | profile paths differ for 5.1, 7 and macOS/Linux; descriptions need PSReadLine `MenuComplete` | `TabExpansion2` in pwsh on all OSes and in Windows PowerShell 5.1 |
| nushell, elvish, xonsh, tcsh, oil, clink | none | snippet from the embedded carapace command | cobra can't generate these | run the snippet's export call |

GitHub runners: ubuntu-24.04 lacks zsh, fish and bash-completion (`apt-get install`); macos-15 has
bash 3.2, zsh and pwsh; windows-2025 has pwsh 7, Windows PowerShell 5.1 and Git Bash.

Testing pattern: chezmoi `testdata/scripts/completion.txtar` runs `exec chezmoi __complete ...` and
compares stdout with a golden file. Repo-specific goldens must stay out of the shared
`completion.txtar`.

### Carapace

Checked in `github.com/carapace-sh/carapace@v1.18.1`:

- `carapace.Gen(root)` adds a hidden `_carapace` command. It sits next to cobra's `completion` and
  `__complete` and reuses existing cobra completion funcs, so nothing gets rewritten.
- `<tool> _carapace nushell` prints a snippet that calls the same binary back. carapace-bin is not
  needed on the user's machine.
- Shells: bash, bash-ble, zsh, fish, powershell, nushell, elvish, xonsh, oil, tcsh, cmd-clink.
- License Apache-2.0; `go-licenses check` passes. `LICENSE.appendix` reserves the `Uid`, `Query` and
  `Trace` features "for Tabdance"; the owner reads it before adoption.
- Cost: two new modules (`carapace`, `carapace-shlex/v2`), about +1 MB on the tfcensus binary.
- Risk: one main maintainer and a release every few weeks; Renovate's minimum release age applies.
- Hazard: `Gen` calls `cobra.OnInitialize` (an unlocked global slice) and fills a global map keyed
  by `*cobra.Command`. Calling it in a constructor that parallel tests use fails `go test -race`.
- If embedded, `carapace.Test(t)` and `pkg/sandbox` help test it.

### Packaging

goreleaser's own repo is the model: `scripts/completions_and_manpages.sh` runs as a before hook,
archives add `completions/*` and `manpages/*`, and the cask uses `completions:` and `manpages:`
(`_reference/goreleaser/goreleaser/.goreleaser.yaml`). Scoop has no completion stanza, so
`post_install` prints the `$PROFILE` line. mise's `github:` backend installs binaries only.
`GenManTree` honours `SOURCE_DATE_EPOCH`, and running it from `scripts/gendocs` keeps go-md2man out
of the release binary.

Not adopted: usage (https://usage.jdx.dev/spec/integrations/cobra). Its Go integration is a
development preview, its pages don't cover cobra's dynamic completion funcs, and its scripts need
the `usage` binary on PATH.

## GitHub Action

Order: CLI v0.1.0, then a CLI release with the features the action needs (M13), then action v1.0.0
pinned to that release (M14). GitHub-only CLI releases are enough, because the action downloads
from GitHub.

Design, the same in both action repos:

- Composite action with a bash installer. It runs on Linux, macOS and Windows runners. A Docker
  action doesn't fit tfcensus, because drift and scan need terragrunt and terraform from the runner.
- Install checks sha256 against `checksums.txt` and, by default, `gh attestation verify` with
  `--signer-workflow sassdavid/<tool>/.github/workflows/release.yml`. cosign and checksum-only are
  options. There is no "none".
- The default `version` is a pin that Renovate bumps through a `# renovate:` comment. `latest`
  resolves through the releases redirect.
- Modes: `install`, `report` (one run writes json, sarif and md, uploads SARIF, writes the step
  summary, applies the gate) and `comment` (report plus the PR comment). relgap's
  `comment-on-renovate` becomes `comment` with `command: digest`.
- The action never takes AWS credentials. Users run `aws-actions/configure-aws-credentials` and
  terragrunt first.
- Inputs: `version`, `verify`, `mode`, `command`, `args` (one per line, passed through env, never
  `${{ }}` in `run:`), `from`, `fail-on`, `sarif`, `step-summary`, `github-token`,
  `working-directory`. Outputs: `version`, `path`, `exit-code`, `json-file`, `sarif-file`,
  `md-file`.
- Security: every `uses:` pinned by SHA, including inside `action.yml`; immutable releases with a
  moving `v1` tag; `permissions: {}` at the top; no `pull_request_target`; fork PRs fall back to the
  step summary. The March 2026 setup-trivy tag rewrite is the reason for the strict pinning.
- The sticky comment lives in the CLI, not in a third-party action: a shared `internal/ghcomment` on
  `internal/httpx`, copying the tfcensus Bitbucket notifier's marker and hash lifecycle. GitHub caps
  a comment at 65,536 characters, so both CLIs truncate at 60,000 characters on section boundaries.
- Marketplace: one `action.yml` at the root and a unique name. CI workflows don't block a listing.

Sources: https://docs.github.com/en/actions/how-tos/create-and-publish-actions/publish-in-github-marketplace,
https://docs.github.com/en/actions/how-tos/create-and-publish-actions/using-immutable-releases-and-tags-to-manage-your-actions-releases,
golangci/golangci-lint-action and aquasecurity/setup-trivy `action.yml`.

## LLM features

What comparable tools do:

- Built-in opt-in call: GoReleaser Pro `changelog.ai`, cds-snc/terraform-plan
  `summarize-with-ai`, Spacelift Saturnhead, Prowler Lighthouse. Prowler documents that resource
  names and account IDs may reach the provider.
- MCP server, no built-in call: Trivy, Snyk, Steampipe (with MCP prompts), Infracost.
- CI step that calls a model: Terrateam hooks, `actions/ai-inference`.
- MCP sampling is deprecated in the 2026-07-28 spec, and Claude Code never supported it. MCP
  prompts and resources are not deprecated.

relgap reads public release-note prose from many sources. A cited summary of "what changes between
1.5 and 1.9" saves real reading time, and every entry has an ID and a URL to cite. tfcensus output
is already a deterministic summary with a rule and reason per finding. The questions an LLM could
help with need attribute values tfcensus must not show, and its identifiers (account IDs, ARNs,
bucket names) map the organization. So tfcensus follows the Trivy and Steampipe pattern.

Guardrails for relgap `--summarize`: off by default with no network call; advisory only, never part
of `--fail-on`, exit codes or SARIF levels; upstream text sent as delimited untrusted data; no repo
paths or pin locations; a hard timeout, and a provider failure is a warning.
