---
description: Install tfcensus from a release archive, with mise or with go install, verify the release signature, and set up man pages and shell completion.
---

# Install

tfcensus is one static binary for Linux, macOS and Windows on amd64 and arm64. It needs no runtime
and makes no network call except to AWS and the notify targets you configure. Then check your setup
with [getting started](/getting-started).

## Channels

| Channel | Command | Completion and man pages |
|---|---|---|
| GitHub release | download `tfcensus_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) from [the releases page](https://github.com/sassdavid/tfcensus/releases) | `completions/` and `manpages/` in the archive |
| mise | `mise use github:sassdavid/tfcensus` | the binary only: generate the script with `tfcensus completion <shell>` |
| go install | `go install github.com/sassdavid/tfcensus/cmd/tfcensus@latest` | the binary only, unsigned |

A Homebrew cask (`sassdavid/tap`), a Scoop bucket and a container image on ghcr.io are not
published yet. The [changelog](/changelog) says when they are.

The [supported versions](/supported-versions) page lists the terragrunt, terraform and OpenTofu
versions tfcensus runs with.

## Verify a release

Each release has a `checksums.txt`, signed with cosign (keyless), and a GitHub build provenance
attestation for every archive. Check the signature, then the archive's checksum and provenance:

```sh
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/sassdavid/tfcensus/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
sha256sum --ignore-missing -c checksums.txt
gh attestation verify --owner sassdavid tfcensus_<version>_<os>_<arch>.tar.gz
```

On macOS, use `shasum -a 256 --ignore-missing -c checksums.txt` for the checksum. The release also
has an SBOM for each archive, and every archive holds `third_party_licenses/` with the license of
each dependency.

## Man pages

The archive holds one man page per command (`manpages/tfcensus.1.gz`, `manpages/tfcensus-scan.1.gz`
and so on). Copy them to a `man1` directory on your `MANPATH`, such as `~/.local/share/man/man1`.
The same text is in the [CLI reference](/cli/).

## Shell completion

The archive holds `completions/tfcensus.bash`, `.zsh`, `.fish` and `.ps1`.
`tfcensus completion <shell>` prints the same scripts, and the scripts for nushell, elvish, xonsh,
tcsh, oil and clink.

Completion reads local files only. `--profile` lists the profiles in `AWS_CONFIG_FILE` or
`~/.aws/config`, `--unit` lists the units of the `--index` file on the same command line, and
`--types` lists the types tfcensus knows. A lookup that takes more than 1 s offers nothing.

### bash

bash needs the bash-completion package. Copy the script, or generate it:

```sh
mkdir -p ~/.local/share/bash-completion/completions
tfcensus completion bash > ~/.local/share/bash-completion/completions/tfcensus
```

macOS ships
bash 3.2 as `/bin/bash`: it needs brew's `bash-completion` (version 1), not `bash-completion@2`,
which needs bash 4.2 or later. With bash before 4.4, a comma list such as
`--fail-on unmanaged,` gets a space after each completed value.

### zsh

Put `_tfcensus` in a directory on `$fpath` before `compinit` runs:

```sh
mkdir -p ~/.zsh/completions
tfcensus completion zsh > ~/.zsh/completions/_tfcensus
# ~/.zshrc, before compinit:
fpath=(~/.zsh/completions $fpath)
autoload -U compinit && compinit
```

If zsh prints
"command not found: compdef", the script loaded before `compinit`. If a new version still completes
the old flags, delete `~/.zcompdump*` and start a new shell. `compaudit` lists group-writable
directories that make `compinit` skip files.

### fish

```sh
tfcensus completion fish > ~/.config/fish/completions/tfcensus.fish
```

Don't
add `tfcensus completion fish | source` to `config.fish`: it runs tfcensus on every shell start.
fish ignores the file extension filter, so `--config` lists every file.

### PowerShell

Save the script and dot-source it from your profile:

```powershell
tfcensus completion powershell | Out-File -Encoding utf8 ~/tfcensus.ps1
Add-Content $PROFILE ". ~/tfcensus.ps1"
```

`$PROFILE` is a
different file in Windows PowerShell 5.1, in PowerShell 7 on Windows and on macOS or Linux, so add
the line to each shell you use. Descriptions show with
`Set-PSReadLineKeyHandler -Key Tab -Function MenuComplete`. PowerShell also ignores the file
extension filter.

### nushell, elvish, xonsh, tcsh, oil and clink

carapace, built into tfcensus, writes these scripts. Nothing else needs installing. Each script
calls tfcensus back to get the completions, so tfcensus must be on `PATH`.

| Shell | Setup |
|---|---|
| nushell | `tfcensus completion nushell \| save --force ~/.config/nushell/tfcensus.nu`, then in `config.nu`: `source ~/.config/nushell/tfcensus.nu` and `$env.config.completions.external = {enable: true, completer: $tfcensus_completer}` |
| elvish | `eval (tfcensus completion elvish \| slurp)` in `~/.config/elvish/rc.elv` |
| xonsh | `exec($(tfcensus completion xonsh))` in `~/.xonshrc` |
| tcsh | ``eval `tfcensus completion tcsh` `` in `~/.tcshrc` |
| oil | `source <(tfcensus completion oil)` in your oils startup file |
| clink | `tfcensus completion clink > %LOCALAPPDATA%\clink\tfcensus.lua` |

The nushell completer handles every external command, so if you already have one, call
`$tfcensus_completer` from it when the first span is `tfcensus`.

### Debugging

`tfcensus __complete <args> ''` prints what the shell would offer, then `:<directive>`.
`BASH_COMP_DEBUG_FILE=/tmp/comp.log` makes the cobra scripts log each request.
