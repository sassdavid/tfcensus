#!/usr/bin/env bash
# Records real terragrunt output for a fixture repo, for the fake terragrunt in tests to replay.
# Every AWS credential source is blanked, so nothing can reach AWS. Run it again after a terragrunt
# upgrade and review the diff.
#   bash scripts/record-terragrunt.sh tg-live
set -euo pipefail

name=${1:?usage: record-terragrunt.sh <fixture under testdata/repos>}
src=testdata/repos/$name
out=testdata/terragrunt/$name
# Terragrunt relativizes paths against the symlink-resolved directory, so resolve it first.
work=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf "$work"' EXIT
cp -R "$src" "$work/$name"
# Stacks in the fixtures take their units from the local catalog.
cp -R testdata/repos/tg-catalog "$work/tg-catalog"

tg() {
  (cd "$work/$name" && env -u AWS_PROFILE -u AWS_DEFAULT_PROFILE -u AWS_ACCESS_KEY_ID \
    -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
    AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true \
    TG_TF_PATH=terraform terragrunt "$@")
}

rm -rf "$out"
mkdir -p "$out/render"
terragrunt --version | head -1 >"$out/VERSION"
flags=(--json --dag --dependencies --exclude --no-color --log-level error)
tg find "${flags[@]}" >"$out/find.json"
tg find "${flags[@]}" --filter './live/prod/**' >"$out/find-prod.json"
for unit in $(jq -r '.[] | select(.type == "unit") | .path' "$out/find.json"); do
  file="$out/render/${unit//\//__}"
  if tg render --working-dir "$unit" --json --no-dependency-outputs --tf-path terraform \
    --no-color --log-level error >"$file.json" 2>"$file.stderr"; then
    rm "$file.stderr"
  else
    rm "$file.json"
  fi
done
# Generated stacks: the units under .terragrunt-stack/ after stack generate.
tg stack generate --no-color --log-level error >/dev/null
tg find "${flags[@]}" >"$out/find-generated.json"
for unit in $(jq -r '.[] | select(.type == "unit") | .path' "$out/find-generated.json" | grep /.terragrunt-stack/); do
  tg render --working-dir "$unit" --json --no-dependency-outputs --tf-path terraform \
    --no-color --log-level error >"$out/render/${unit//\//__}.json"
done
# Absolute temp paths would make the recording differ on every run. Paths inside the repo become
# repo-relative, as tfcensus prints them.
find "$out" -type f -exec sed -i.bak -e "s|$work/$name/||g" -e "s|$work|/fixture|g" {} +
find "$out" -name '*.bak' -delete
echo "recorded $out"
