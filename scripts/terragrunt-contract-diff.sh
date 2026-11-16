#!/usr/bin/env bash
# Records the terragrunt on PATH against a fixture into a temp directory and prints a diff against
# the checked-in recording, to $GITHUB_STEP_SUMMARY in GitHub Actions. It's information only and
# exits 0 on a difference: TestTerragruntContract decides whether the difference matters.
#   bash scripts/terragrunt-contract-diff.sh tg-live
set -euo pipefail

name=${1:?usage: terragrunt-contract-diff.sh <fixture under testdata/repos>}
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
bash scripts/record-terragrunt.sh "$name" "$out/new" >/dev/null
cp -R "testdata/terragrunt/$name" "$out/recorded"
# terragrunt starts each stderr line with the time of day.
find "$out/recorded" "$out/new" -name '*.stderr' -exec sed -i.bak -E 's/^[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3} //' {} +
find "$out" -name '*.bak' -delete

summary=${GITHUB_STEP_SUMMARY:-/dev/stdout}
{
  echo "### terragrunt recording diff: $name"
  echo
  if (cd "$out" && diff -ru recorded new >changes.diff); then
    echo "$(cat "$out/new/VERSION") writes the same output as the recording."
  else
    echo "The recording is from $(cat "$out/recorded/VERSION"); this run used $(cat "$out/new/VERSION")."
    echo
    echo '```diff'
    cat "$out/changes.diff"
    echo '```'
  fi
} >>"$summary"
