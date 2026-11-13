# Install and shell completion

## Channels

| Channel | Command | Completion and man pages |
|---|---|---|
| GitHub release | download `tfcensus_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) | `completions/` and `manpages/` in the archive |
| mise | `mise use github:sassdavid/tfcensus` | the binary only: generate the script with `tfcensus completion <shell>` |
| go install | `go install github.com/sassdavid/tfcensus/cmd/tfcensus@latest` | the binary only, unsigned |
| Homebrew cask (from M19) | `brew install --cask sassdavid/tap/tfcensus` | installed for bash, zsh and fish, plus every man page |
| Scoop (from M19) | `scoop bucket add sassdavid https://github.com/sassdavid/scoop-bucket`, then `scoop install tfcensus` | prints the line for your PowerShell profile |
| Container (from M19) | `docker run --rm ghcr.io/sassdavid/tfcensus version` | none |

The archive holds `completions/tfcensus.bash`, `.zsh`, `.fish` and `.ps1`, and one man page per
command (`manpages/tfcensus.1.gz`, `manpages/tfcensus-scan.1.gz` and so on). To read the man pages
from an archive, copy them to a `man1` directory on your `MANPATH`, such as
`~/.local/share/man/man1`.

Completion reads local files only. `--profile` lists the profiles in `AWS_CONFIG_FILE` or
`~/.aws/config`, `--unit` lists the units of the `--index` file on the same command line, and
`--types` lists the types tfcensus knows. A lookup that takes more than 1 s offers nothing.

## bash

bash needs the bash-completion package. Copy the script, or generate it:

```sh
mkdir -p ~/.local/share/bash-completion/completions
tfcensus completion bash > ~/.local/share/bash-completion/completions/tfcensus
```

The Homebrew cask installs it to `$(brew --prefix)/etc/bash_completion.d/tfcensus`. macOS ships
bash 3.2 as `/bin/bash`: it needs brew's `bash-completion` (version 1), not `bash-completion@2`,
which needs bash 4.2 or later. With bash before 4.4, a comma list such as
`--fail-on unmanaged,` gets a space after each completed value.

## zsh

Put `_tfcensus` in a directory on `$fpath` before `compinit` runs:

```sh
mkdir -p ~/.zsh/completions
tfcensus completion zsh > ~/.zsh/completions/_tfcensus
# ~/.zshrc, before compinit:
fpath=(~/.zsh/completions $fpath)
autoload -U compinit && compinit
```

The cask installs it to `$(brew --prefix)/share/zsh/site-functions/_tfcensus`. If zsh prints
"command not found: compdef", the script loaded before `compinit`. If a new version still completes
the old flags, delete `~/.zcompdump*` and start a new shell. `compaudit` lists group-writable
directories that make `compinit` skip files.

## fish

```sh
tfcensus completion fish > ~/.config/fish/completions/tfcensus.fish
```

The cask installs it to `$(brew --prefix)/share/fish/vendor_completions.d/tfcensus.fish`. Don't
add `tfcensus completion fish | source` to `config.fish`: it runs tfcensus on every shell start.
fish ignores the file extension filter, so `--config` lists every file.

## PowerShell

Save the script and dot-source it from your profile:

```powershell
tfcensus completion powershell | Out-File -Encoding utf8 ~/tfcensus.ps1
Add-Content $PROFILE ". ~/tfcensus.ps1"
```

With Scoop, dot-source the script from the `current` directory, which Scoop moves to each new
version. `scoop install` prints the line, such as
`. "$env:USERPROFILE\scoop\apps\tfcensus\current\completions\tfcensus.ps1"`. `$PROFILE` is a
different file in Windows PowerShell 5.1, in PowerShell 7 on Windows and on macOS or Linux, so add
the line to each shell you use. Descriptions show with
`Set-PSReadLineKeyHandler -Key Tab -Function MenuComplete`. PowerShell also ignores the file
extension filter.

## nushell, elvish, xonsh, tcsh, oil and clink

carapace, built into tfcensus, writes these scripts. Nothing else needs installing. Each script
calls `tfcensus _carapace` back, so tfcensus must be on `PATH`.

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

## Debugging

`tfcensus __complete <args> ''` prints what the shell would offer, then `:<directive>`.
`BASH_COMP_DEBUG_FILE=/tmp/comp.log` makes the cobra scripts log each request.
