#!/usr/bin/env bash
# Collects the license notices of everything compiled into the release binaries, for every release
# platform, into third_party_licenses/. goreleaser runs it as a before hook and puts the directory
# into each archive. Fails on forbidden, restricted (GPL-style) or unrecognized licenses.
#   bash scripts/licenses.sh           # check, then write third_party_licenses/
#   bash scripts/licenses.sh -check    # check only (mise run lint:licenses)
#
# MPL-2.0 needs the source of covered code to be available. Its FAQ (Q8) says pointing recipients
# to the source of unmodified MPL code is enough, so THIRD_PARTY_NOTICES.md links every module's
# source zip on the Go module mirror instead of copying the source into the archives.
set -euo pipefail

check_only=false
for arg in "$@"; do
  case "$arg" in
    -check | --check) check_only=true ;;
    *)
      echo "licenses: unknown argument $arg (want -check)" >&2
      exit 2
      ;;
  esac
done

module=$(sed -n 's/^module //p' go.mod)
tool=$(basename "$module")
# Must match the goos/goarch matrix in .goreleaser.yaml. Some modules are compiled in for one OS
# only (cobra uses mousetrap on Windows), so every platform is collected.
platforms=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)
out=third_party_licenses

# go-licenses finds the standard library through GOROOT; point it at the toolchain in use.
GOROOT=$(go env GOROOT)
export GOROOT

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# go-licenses warns about assembly files in golang.org/x/sys on every run, so its stderr is shown
# only when it fails.
quiet() {
  if ! "$@" 2>"$tmp/stderr"; then
    cat "$tmp/stderr" >&2
    return 1
  fi
}

for p in "${platforms[@]}"; do
  GOOS=${p%/*} GOARCH=${p#*/} quiet go-licenses check "./cmd/$tool" --ignore "$module" \
    --disallowed_types=forbidden,restricted,unknown
done
if $check_only; then
  echo "licenses: every dependency of ./cmd/$tool has an allowed license on ${#platforms[@]} platforms"
  exit 0
fi

rm -rf "$out"
mkdir -p "$out"
printf '%s\n' '{{range .}}{{.Name}}|{{.Version}}|{{.LicenseName}}{{"\n"}}{{end}}' >"$tmp/report.tmpl"
for p in "${platforms[@]}"; do
  dir=$tmp/save/${p/\//_}
  GOOS=${p%/*} GOARCH=${p#*/} quiet go-licenses save "./cmd/$tool" --ignore "$module" --save_path "$dir"
  GOOS=${p%/*} GOARCH=${p#*/} quiet go-licenses report "./cmd/$tool" --ignore "$module" \
    --template "$tmp/report.tmpl" >>"$tmp/report.txt"
  # Files come from the read-only module cache; later platforms overwrite the same files.
  chmod -R u+w "$dir"
  cp -R "$dir/." "$out/"
done
# For reciprocal licenses go-licenses copies the whole module; keep only the notices.
find "$out" -type f ! \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'NOTICE*' \
  -o -iname 'COPYING*' -o -iname 'PATENTS*' -o -iname 'UNLICENSE*' \) -delete
find "$out" -type d -empty -delete

mkdir -p "$out/go"
cp "$GOROOT/LICENSE" "$out/go/LICENSE"
if [[ -f "$GOROOT/PATENTS" ]]; then
  cp "$GOROOT/PATENTS" "$out/go/PATENTS"
fi

# Data embedded from other projects keeps its license next to it as <name>.LICENSE.
data=()
while IFS= read -r f; do
  data+=("$f")
done < <(git ls-files '*.LICENSE')
if [[ ${#data[@]} -gt 0 ]]; then
  mkdir -p "$out/data"
  for f in "${data[@]}"; do
    cp "$f" "$out/data/$(basename "$f")"
  done
fi

go list -m -f '{{.Path}}' all >"$tmp/modules"
{
  echo "# Third-party notices for $tool"
  echo
  echo "$tool is built from the Go modules below and the Go standard library. This directory holds"
  echo "their license, notice and patent files, collected for every release platform. Each module's"
  echo "source, at the version compiled in, is the zip on the Go module mirror in the Source column."
  echo
  echo "The Go standard library ($(go env GOVERSION)) is under go/LICENSE and go/PATENTS."
  echo
  echo "| Module | Version | License | Source |"
  echo "|---|---|---|---|"
  # Packages map to the longest module path that contains them; the mirror escapes capitals as !x.
  sort -u "$tmp/report.txt" | awk -F'|' -v modules="$tmp/modules" '
    BEGIN { while ((getline m < modules) > 0) known[m] = 1 }
    NF == 3 {
      mod = ""
      for (m in known) {
        if (($1 == m || index($1, m "/") == 1) && length(m) > length(mod)) mod = m
      }
      if (mod == "") mod = $1
      key = mod "|" $2
      if (!(key in lic)) { lic[key] = $3; order[++n] = key }
      else if (index(", " lic[key] ", ", ", " $3 ", ") == 0) lic[key] = lic[key] ", " $3
    }
    END {
      for (i = 1; i <= n; i++) {
        split(order[i], kv, "|")
        esc = ""
        for (j = 1; j <= length(kv[1]); j++) {
          c = substr(kv[1], j, 1)
          esc = esc (c ~ /[A-Z]/ ? "!" tolower(c) : c)
        }
        printf "| %s | %s | %s | https://proxy.golang.org/%s/@v/%s.zip |\n", kv[1], kv[2], lic[order[i]], esc, kv[2]
      }
    }' | sort
  if [[ ${#data[@]} -gt 0 ]]; then
    echo
    echo "Embedded data from other projects, with the license of each:"
    echo
    for f in "${data[@]}"; do
      echo "- data/$(basename "$f"), from $f in the $tool repository"
    done
  fi
} >"$out/THIRD_PARTY_NOTICES.md"

echo "licenses: wrote $out/ ($(find "$out" -type f | wc -l | tr -d ' ') files)"
