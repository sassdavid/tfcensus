# Sibling repositories

tfcensus (`~/src/github/tfcensus`) and relgap (`~/src/github/relgap`) are built, tested and
released the same way. So are their GitHub Actions, tfcensus-action (`~/src/github/tfcensus-action`)
and relgap-action (`~/src/github/relgap-action`). Prefer the solution the sibling repo already has:
copy its file and change only the tool name. Write something new only when neither repo has it,
then add it to both.

## What must match

`scripts/sibling-diff.sh` lists the shared files, with one list for the tool repos and one for
the action repos. It takes the repo name from a `.sibling` file or the directory name. Files on its
"verbatim" list (this file and the script) are byte-identical in all four repos. Files on an
"identical" list match once tfcensus/relgap, TFCENSUS/RELGAP and Tfcensus/Relgap are swapped. An
action repo also compares the verbatim files and `.claude/rules/writing.md` with its tool's repo.
Run `mise run sibling:diff` after changing any of them, and make the same change in the sibling
repo in the same session. A change to a verbatim file goes to all four repos. If you can't, say so
in your final message.

The "similar" files share structure but differ on purpose: go.mod, mise.toml, `.goreleaser.yaml`,
the release workflows, the release-please manifest, the root command and the Config struct,
CLAUDE.md, README.md and the engineering docs. In the action repos they are CLAUDE.md, README.md,
mise.toml, mise.lock, the manifest and `scripts/tool.env`. `mise run sibling:diff -- -all` shows
those differences.

Keep the same in both siblings:

- The Go version (go directive and toolchain), direct dependency versions and indirect bumps.
- Tool versions in mise.toml, action SHAs, and the mise version passed to `jdx/mise-action`.
- The golden golangci-lint config tag and the local overrides (ADR-0008).
- Comment and writing rules: `.claude/rules/go.md` and `.claude/rules/writing.md`.
- AWS SDK rules: `.claude/rules/aws-sdk.md`.

## Release process and accounts

- release-please opens the release PR on `main`. Merging it creates the tag and a draft release,
  then calls `release.yml` with `workflow_call`. Tags pushed with `GITHUB_TOKEN` don't trigger
  workflows, so there is no tag-triggered release in normal use.
- goreleaser uploads the archives, checksums, SBOMs and the cosign bundle to that draft and
  publishes it. `actions/attest` adds build provenance for every archive.
- In an action repo, release-please creates a draft `vX.Y.Z` release and its tag. The owner
  publishes it in the web UI, where the Marketplace box is, and release immutability locks it.
  `major-tag.yml` then moves the `v1` tag, which never gets a release of its own.
- Every archive carries `third_party_licenses/` from `scripts/licenses.sh`, which goreleaser runs
  as a before hook. `mise run lint:licenses` fails on forbidden, restricted or unknown licenses.
  Embedded data from another project keeps its license next to it as `<name>.LICENSE`
  (engineering ADR-0012).
- The Homebrew cask goes to `sassdavid/homebrew-tap` and the Scoop manifest to
  `sassdavid/scoop-bucket`.
- Repository secrets have the same names in both repos: `HOMEBREW_TAP_TOKEN` and
  `SCOOP_BUCKET_TOKEN`. `GITHUB_TOKEN` covers the release, the attestations and ghcr.io.

## Intended differences

- tfcensus only: the ghcr.io image (`dockers_v2`, Dockerfile, the docker steps in `release.yml`),
  `docs/iam/`, `--tf-path` and `--tg-path`.
- relgap only: `--summarize` and its `llm` config, which send extracted release-note entries to a
  model the user configures (relgap's `docs/plan/specs/mcp-and-llm.md`).
- tfcensus only: no built-in LLM call. Agents use its MCP tools and the prompts `triage_drift` and
  `explain_unmanaged`, and `docs/ci/ai-summary.md` shows an opt-in CI step (tfcensus plan ADR-0013).
- Each repo has its own Config struct, commands, specs and `docs/plan/`, and registers its own
  MCP tools and prompts on the shared `internal/mcpkit`.
- Each docs site has its own identity: `theme/tokens.css`, `theme/site.css`, `theme/components/`,
  `docs/index.md`, the images in `docs/public/` and the site data generators in `scripts/gensite/`
  (engineering ADR-0016). The build, workflows and checks are shared.
- Each action repo has its own `scripts/tool.env` (tool name, repository, pinned version) and
  README. The milestones for both actions are in the tool repos' `docs/plan/milestones.md` (M14).
