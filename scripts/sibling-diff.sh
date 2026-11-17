#!/usr/bin/env bash
# Compares the files shared by sibling repositories (.claude/rules/siblings.md): tfcensus with
# relgap, and tfcensus-action with relgap-action. Verbatim files must be byte-identical in all
# four repos; the identical ones must match once the tool name is swapped. An action repo also
# compares its verbatim files and writing rules with its tool's repo (relgap-action with relgap).
# The repo name comes from a .sibling file or the directory name. Exits 1 when a file that must
# match differs.
#   mise run sibling:diff             # identical files only
#   mise run sibling:diff -- -all     # also show the expected differences in similar files
set -euo pipefail

verbatim=(
  .claude/rules/renovate.md
  .claude/rules/siblings.md
  scripts/sibling-diff.sh
)

cli_identical=(
  .claude/rules/aws-sdk.md
  .claude/rules/go.md
  .claude/rules/writing.md
  .gitattributes
  .github/jactionlint.yaml
  .github/workflows/aube-lock.yml
  .github/workflows/ci.yml
  .github/workflows/docs.yml
  .github/workflows/renovate-config.yml
  .github/workflows/renovate.yml
  .github/workflows/security.yml
  .golangci.yml
  .gitignore
  docs/.oxfmtrc.json
  docs/.vitepress/contrast.test.mjs
  docs/.vitepress/theme/index.ts
  docs/.vitepress/tokens.mjs
  docs/aube-workspace.yaml
  mise.docs.lock
  mise.docs.toml
  mise.lock
  release-please-config.json
  renovate.json
  internal/app/app_test.go
  internal/app/deps.go
  internal/app/doc.go
  internal/app/logger.go
  internal/app/vt_other.go
  internal/app/vt_windows.go
  internal/buildinfo/buildinfo.go
  internal/buildinfo/doc.go
  internal/cli/completion.go
  internal/cli/completion_test.go
  internal/cli/out.go
  internal/cli/run.go
  internal/cli/version.go
  internal/cli/version_test.go
  internal/complete/complete.go
  internal/complete/complete_test.go
  internal/complete/doc.go
  internal/config/load.go
  internal/config/paths.go
  internal/config/schema_test.go
  internal/config/validate.go
  internal/execx/doc.go
  internal/execx/execx.go
  internal/execx/execx_test.go
  internal/execx/procattr_unix.go
  internal/execx/procattr_windows.go
  internal/execx/redact.go
  internal/ghcomment/doc.go
  internal/ghcomment/ghcomment.go
  internal/ghcomment/ghcomment_test.go
  internal/httpx/httpx.go
  internal/httpx/httpx_test.go
  internal/mcpkit/doc.go
  internal/mcpkit/mcpkit_test.go
  internal/mcpkit/page.go
  internal/mcpkit/prompt.go
  internal/mcpkit/prompt_test.go
  internal/mcpkit/roots.go
  internal/mcpkit/server.go
  internal/prose/doc.go
  internal/prose/prose.go
  internal/prose/prose_test.go
  internal/render/doc.go
  internal/render/out.go
  internal/render/out_test.go
  internal/render/render.go
  internal/render/render_test.go
  internal/render/truncate.go
  internal/render/truncate_test.go
  internal/sarif/doc.go
  internal/sarif/github.go
  internal/sarif/github_test.go
  internal/sarif/sarif.go
  internal/sarif/sarif_test.go
  internal/testutil/doc.go
  internal/testutil/fakebin/doc.go
  internal/testutil/fakebin/fakebin.go
  internal/testutil/golden.go
  internal/testutil/mcptest/doc.go
  internal/testutil/mcptest/mcptest.go
  internal/testutil/sarifcheck/doc.go
  internal/testutil/sarifcheck/sarif-schema-2.1.0.LICENSE
  internal/testutil/sarifcheck/sarif-schema-2.1.0.json
  internal/testutil/sarifcheck/sarifcheck.go
  internal/testutil/sarifcheck/sarifcheck_test.go
  internal/testutil/vcr/doc.go
  internal/testutil/vcr/vcr.go
  internal/testutil/vcr/vcr_test.go
  internal/yamlschema/doc.go
  internal/yamlschema/yamlschema.go
  internal/yamlschema/yamlschema_test.go
  schemas/doc.go
  schemas/report-v1.json
  schemas/schemas.go
  scripts/completion-shells.ps1
  scripts/completion-shells.sh
  scripts/completions.sh
  scripts/fuzz.sh
  scripts/gendocs/doc.go
  scripts/gendocs/main.go
  scripts/licenses.sh
  scripts/proselint/doc.go
  scripts/proselint/main.go
  scripts/proselint/main_test.go
  scripts/test-golden.sh
  testdata/script/completion.txtar
  testdata/script/root.txtar
  testdata/script/version.txtar
)

cli_similar=(
  CLAUDE.md
  README.md
  .release-please-manifest.json
  go.mod
  mise.toml
  .goreleaser.yaml
  .github/workflows/release.yml
  .github/workflows/release-please.yml
  internal/cli/config.go
  internal/complete/sources.go
  internal/cli/root.go
  internal/cli/script_test.go
  internal/config/config.go
  internal/config/doc.go
  internal/config/load_test.go
  docs/engineering/ci-and-release.md
  docs/engineering/stack.md
  docs/engineering/version-policy.md
  docs/engineering/adr/0008-lint-config.md
  docs/engineering/adr/0013-shell-completion-and-man-pages.md
  docs/install.md
  docs/package.json
  docs/.vitepress/config.mts
  docs/.vitepress/sidebar.mts
  docs/engineering/adr/0016-docs-site.md
)

action_identical=(
  .claude/rules/writing.md
  .gitattributes
  .github/jactionlint.yaml
  .github/workflows/ci.yml
  .github/workflows/major-tag.yml
  .github/workflows/release-please.yml
  .github/workflows/renovate-config.yml
  .github/workflows/renovate.yml
  .github/workflows/security.yml
  .gitignore
  .shellcheckrc
  action.yml
  release-please-config.json
  renovate.json
  scripts/install.sh
  scripts/major-tag.sh
  scripts/run.sh
  scripts/test-install.sh
  scripts/test-outputs.sh
  scripts/test-run.sh
)

action_similar=(
  CLAUDE.md
  README.md
  .release-please-manifest.json
  mise.toml
  mise.lock
  scripts/tool.env
)

# Files an action repo shares byte for byte with its tool's repo.
action_from_tool=(
  "${verbatim[@]}"
  .claude/rules/writing.md
)

show_all=false
for arg in "$@"; do
  case "$arg" in
    -all | --all) show_all=true ;;
    *)
      echo "sibling:diff: unknown argument $arg (want -all)" >&2
      exit 2
      ;;
  esac
done

if [[ -f .sibling ]]; then
  name=$(tr -d '[:space:]' <.sibling)
else
  name=$(basename "$PWD")
fi
self=${name%-action}
suffix=${name#"$self"}
case "$self" in
  tfcensus) other=relgap ;;
  relgap) other=tfcensus ;;
  *)
    echo "sibling:diff: unknown repo $name (want tfcensus, relgap or their -action repos; write the name to .sibling)" >&2
    exit 2
    ;;
esac
if [[ -z "$suffix" ]]; then
  identical=("${cli_identical[@]}")
  similar=("${cli_similar[@]}")
else
  identical=("${action_identical[@]}")
  similar=("${action_similar[@]}")
fi
parent=$(dirname "$PWD")
other_dir=${SIBLING_DIR:-$parent/$other$suffix}
if [[ ! -d "$other_dir" ]]; then
  echo "sibling:diff: $other_dir not found (set SIBLING_DIR)" >&2
  exit 2
fi

upper() { printf '%s' "$1" | tr '[:lower:]' '[:upper:]'; }
title() { printf '%s%s' "$(upper "${1:0:1}")" "${1:1}"; }
rename() {
  sed -e "s/$other/$self/g" -e "s/$(upper "$other")/$(upper "$self")/g" \
    -e "s/$(title "$other")/$(title "$self")/g" "$1"
}

status=0
tool_dir=""
# compare <label> <dir> <rename|cat> <file>...
compare() {
  local label=$1 dir=$2 filter=$3 f
  shift 3
  for f in "$@"; do
    if [[ ! -f "$f" || ! -f "$dir/$f" ]]; then
      echo "missing: $f ($label)"
      status=1
    elif ! diff -u --label "$label/$f" --label "$name/$f" <("$filter" "$dir/$f") "$f"; then
      status=1
    fi
  done
}

compare "$other$suffix" "$other_dir" cat "${verbatim[@]}"
compare "$other$suffix" "$other_dir" rename "${identical[@]}"
if [[ -n "$suffix" ]]; then
  tool_dir=${TOOL_DIR:-$parent/$self}
  if [[ -d "$tool_dir" ]]; then
    compare "$self" "$tool_dir" cat "${action_from_tool[@]}"
  else
    echo "sibling:diff: $tool_dir not found, skipped the comparison with $self (set TOOL_DIR)" >&2
    tool_dir=""
  fi
fi
if $show_all; then
  for f in "${similar[@]}"; do
    if [[ -f "$f" && -f "$other_dir/$f" ]]; then
      diff -u --label "$other$suffix/$f" --label "$name/$f" <(rename "$other_dir/$f") "$f" || true
    fi
  done
fi
if [[ $status -eq 0 ]]; then
  echo "shared files match $other_dir${tool_dir:+ and $tool_dir}"
fi
exit "$status"
