#!/usr/bin/env bash
# Writes the shell completion scripts to completions/ and the man pages to manpages/ for the
# release archives (engineering ADR-0013). goreleaser runs it as a before hook. The man page
# dates come from SOURCE_DATE_EPOCH, else the last commit, so a rerun writes the same bytes.
#   bash scripts/completions.sh
set -euo pipefail

tool=$(basename "$(sed -n 's/^module //p' go.mod)")
if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then
  SOURCE_DATE_EPOCH=$(git log -1 --format=%ct 2>/dev/null || echo 0)
fi
export SOURCE_DATE_EPOCH

rm -rf completions manpages
mkdir completions
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
go build -o "$bin/$tool" "./cmd/$tool"
for sh in bash zsh fish; do
  "$bin/$tool" completion "$sh" >"completions/$tool.$sh"
done
"$bin/$tool" completion powershell >"completions/$tool.ps1"
go run ./scripts/gendocs -man manpages
