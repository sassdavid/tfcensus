#!/usr/bin/env bash
# Compares the files shared by tfcensus and relgap (.claude/rules/siblings.md). Files that name
# both tools must be byte-identical; the rest must match once the tool name is swapped. Exits 1
# when a file that must match differs.
#   mise run sibling:diff             # identical files only
#   mise run sibling:diff -- -all     # also show the expected differences in similar files
set -euo pipefail

verbatim=(
  .claude/rules/siblings.md
  scripts/sibling-diff.sh
)

identical=(
  .claude/rules/go.md
  .claude/rules/writing.md
  .gitattributes
  .github/zizmor.yml
  .github/workflows/ci.yml
  .github/workflows/security.yml
  .golangci.yml
  .gitignore
  .release-please-manifest.json
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
  internal/cli/run.go
  internal/cli/version.go
  internal/cli/version_test.go
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
  internal/prose/doc.go
  internal/prose/prose.go
  internal/prose/prose_test.go
  internal/render/doc.go
  internal/render/render.go
  internal/render/render_test.go
  internal/testutil/doc.go
  internal/testutil/fakebin/doc.go
  internal/testutil/fakebin/fakebin.go
  internal/testutil/golden.go
  internal/testutil/vcr/doc.go
  internal/testutil/vcr/vcr.go
  internal/testutil/vcr/vcr_test.go
  internal/yamlschema/doc.go
  internal/yamlschema/yamlschema.go
  internal/yamlschema/yamlschema_test.go
  schemas/doc.go
  schemas/report-v1.json
  schemas/schemas.go
  scripts/fuzz.sh
  scripts/gendocs/doc.go
  scripts/gendocs/main.go
  scripts/licenses.sh
  scripts/proselint/doc.go
  scripts/proselint/main.go
  scripts/test-golden.sh
  testdata/script/completion.txtar
  testdata/script/root.txtar
  testdata/script/version.txtar
)

similar=(
  CLAUDE.md
  README.md
  go.mod
  mise.toml
  .goreleaser.yaml
  .github/workflows/release.yml
  .github/workflows/release-please.yml
  internal/cli/config.go
  internal/cli/root.go
  internal/cli/script_test.go
  internal/config/config.go
  internal/config/doc.go
  internal/config/load_test.go
  docs/engineering/ci-and-release.md
  docs/engineering/stack.md
  docs/engineering/version-policy.md
  docs/engineering/adr/0008-lint-config.md
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

self=$(basename "$(sed -n 's/^module //p' go.mod)")
case "$self" in
  tfcensus) other=relgap ;;
  relgap) other=tfcensus ;;
  *)
    echo "sibling:diff: unknown module $self" >&2
    exit 2
    ;;
esac
other_dir=${SIBLING_DIR:-$(dirname "$PWD")/$other}
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
for f in "${verbatim[@]}"; do
  if [[ ! -f "$f" || ! -f "$other_dir/$f" ]]; then
    echo "missing: $f"
    status=1
  elif ! diff -u --label "$other/$f" --label "$self/$f" "$other_dir/$f" "$f"; then
    status=1
  fi
done
for f in "${identical[@]}"; do
  if [[ ! -f "$f" || ! -f "$other_dir/$f" ]]; then
    echo "missing: $f"
    status=1
  elif ! diff -u --label "$other/$f" --label "$self/$f" <(rename "$other_dir/$f") "$f"; then
    status=1
  fi
done
if $show_all; then
  for f in "${similar[@]}"; do
    if [[ -f "$f" && -f "$other_dir/$f" ]]; then
      diff -u --label "$other/$f" --label "$self/$f" <(rename "$other_dir/$f") "$f" || true
    fi
  done
fi
if [[ $status -eq 0 ]]; then
  echo "shared files match $other_dir"
fi
exit "$status"
