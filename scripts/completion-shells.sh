#!/usr/bin/env bash
# Loads the tfcensus completion script into a real shell and completes `tfcensus --format `.
# Fails unless json is offered. CI runs it per shell (engineering ADR-0013); tfcensus must be on
# PATH.
#   bash scripts/completion-shells.sh bash|zsh|fish|nushell
#   BASH_BIN=/bin/bash bash scripts/completion-shells.sh bash    # macOS bash 3.2
set -euo pipefail

shell=${1:?usage: completion-shells.sh bash|zsh|fish|nushell}
tool=tfcensus
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

case "$shell" in
  bash)
    bash_bin=${BASH_BIN:-bash}
    bc=${BASH_COMPLETION:-}
    if [[ -z "$bc" ]]; then
      for f in /usr/share/bash-completion/bash_completion \
        /opt/homebrew/etc/bash_completion /usr/local/etc/bash_completion; do
        if [[ -f "$f" ]]; then
          bc=$f
          break
        fi
      done
    fi
    [[ -n "$bc" ]] || {
      echo "bash-completion not found (set BASH_COMPLETION)" >&2
      exit 1
    }
    "$tool" completion bash >"$work/$tool.bash"
    # shellcheck disable=SC2016 # expanded by the inner shell
    out=$("$bash_bin" --norc --noprofile -c '
      source "$1"
      source "$2"
      complete -p "$3" >&2
      COMP_LINE="$3 --format "
      COMP_POINT=${#COMP_LINE}
      COMP_WORDS=("$3" --format "")
      COMP_CWORD=2
      COMP_TYPE=9
      "__start_$3" "$3" "" --format
      printf "%s\n" "${COMPREPLY[@]}"
    ' _ "$bc" "$work/$tool.bash" "$tool")
    "$bash_bin" --version | head -1 >&2
    ;;
  zsh)
    mkdir "$work/fpath"
    "$tool" completion zsh >"$work/fpath/_$tool"
    # shellcheck disable=SC2016 # expanded by zsh
    zsh -f -c '
      fpath=("$1" $fpath)
      autoload -U compinit
      compinit -u -d "$1/.zcompdump"
      print -r -- "registered: $_comps[$2]" >&2
      [[ $_comps[$2] == _$2 ]] || exit 1
    ' _ "$work/fpath" "$tool"
    # compinit only registers the function, so ask the binary the question the function asks.
    out=$("$tool" __complete --format '')
    ;;
  fish)
    "$tool" completion fish >"$work/$tool.fish"
    out=$(fish --no-config -c "source '$work/$tool.fish'; complete -C'$tool --format '")
    ;;
  nushell)
    "$tool" completion nushell >"$work/$tool.nu"
    out=$(nu --no-config-file -c "source '$work/$tool.nu'; do \$${tool}_completer ['$tool' '--format' ''] | get value | str join (char newline)")
    ;;
  *)
    echo "unknown shell $shell" >&2
    exit 2
    ;;
esac

printf '%s\n' "$out"
if ! grep -q '^json' <<<"$out"; then
  echo "$shell: completing '$tool --format ' did not offer json" >&2
  exit 1
fi
echo "$shell: ok"
