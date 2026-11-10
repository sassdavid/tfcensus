# Sibling repositories

tfcensus (`~/src/github/tfcensus`) and relgap (`~/src/github/relgap`) are built, tested and
released the same way. Prefer the solution the other repo already has: copy its file and change
only the tool name. Write something new only when neither repo has it, then add it to both.

## What must match

`scripts/sibling-diff.sh` lists the shared files. Files on its "verbatim" list (this file and the
script) are byte-identical. Files on its "identical" list match once tfcensus/relgap,
TFCENSUS/RELGAP and Tfcensus/Relgap are swapped. Run `mise run sibling:diff` after changing any of
them, and make the same change in the other repo in the same session. If you can't, say so in your
final message.

The "similar" files share structure but differ on purpose: go.mod, mise.toml, `.goreleaser.yaml`,
the release workflows, the root command and the Config struct, CLAUDE.md, README.md and the
engineering docs. `mise run sibling:diff -- -all` shows those differences.

Keep the same in both:

- The Go version (go directive and toolchain), direct dependency versions and indirect bumps.
- Tool versions in mise.toml, action SHAs, and the mise version passed to `jdx/mise-action`.
- The golden golangci-lint config tag and the local overrides (ADR-0008).
- Comment and writing rules: `.claude/rules/go.md` and `.claude/rules/writing.md`.

## Release process and accounts

- release-please opens the release PR on `main`. Merging it creates the tag and a draft release,
  then calls `release.yml` with `workflow_call`. Tags pushed with `GITHUB_TOKEN` don't trigger
  workflows, so there is no tag-triggered release in normal use.
- goreleaser uploads the archives, checksums, SBOMs and the cosign bundle to that draft and
  publishes it. `actions/attest` adds build provenance for every archive.
- The Homebrew cask goes to `sassdavid/homebrew-tap` and the Scoop manifest to
  `sassdavid/scoop-bucket`.
- Repository secrets have the same names in both repos: `HOMEBREW_TAP_TOKEN` and
  `SCOOP_BUCKET_TOKEN`. `GITHUB_TOKEN` covers the release, the attestations and ghcr.io.

## Intended differences

- tfcensus only: the ghcr.io image (`dockers_v2`, Dockerfile, the docker steps in `release.yml`),
  `docs/iam/`, `--tf-path` and `--tg-path`.
- Each repo has its own Config struct, commands, specs and `docs/plan/`.
