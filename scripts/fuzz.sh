#!/usr/bin/env bash
# Runs every Go fuzz target (func FuzzXxx in a _test.go file) for a fixed time each. Exits 0
# when there are no targets.
#   mise run fuzz            # 60s per target
#   mise run fuzz -- 10s     # another duration
set -euo pipefail

fuzztime=${1:-60s}

targets=$(grep -rEo --include='*_test.go' '^func Fuzz[A-Za-z0-9_]*\(' . | sed -E 's/\($//' || true)
if [[ -z "$targets" ]]; then
  echo "fuzz: no fuzz targets"
  exit 0
fi

while IFS= read -r line; do
  file=${line%%:*}
  name=${line##*func }
  pkg=./$(dirname "${file#./}")
  echo "fuzz: $pkg $name for $fuzztime"
  go test -run '^$' -fuzz "^${name}\$" -fuzztime "$fuzztime" "$pkg"
done <<<"$targets"
