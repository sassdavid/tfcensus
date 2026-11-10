#!/usr/bin/env bash
# Runs the tests that compare against golden files.
#   mise run test:golden            # compare
#   mise run test:golden -- -update # rewrite goldens, then review `git diff`
set -euo pipefail

for arg in "$@"; do
  case "$arg" in
    -update | --update) export GOLDEN_UPDATE=1 ;;
    *)
      echo "test:golden: unknown argument $arg (want -update)" >&2
      exit 2
      ;;
  esac
done

go test -count=1 ./...

if [[ -n "${GOLDEN_UPDATE:-}" ]]; then
  echo "golden files updated; review them with: git diff -- schemas testdata" >&2
fi
