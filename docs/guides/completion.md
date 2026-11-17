---
description: Set up tab completion for tfcensus in bash, zsh, fish, PowerShell, nushell, elvish, xonsh, tcsh, oil and clink, and debug it when it offers nothing.
---

# Shell completion

tfcensus completes its commands, flags and flag values, and some values from your files: AWS
profiles, units and resource types. The release archive holds the scripts for bash, zsh, fish and
PowerShell, in `completions/tfcensus.bash`, `.zsh`, `.fish` and `.ps1`. `tfcensus completion <shell>`
prints the same scripts, and the scripts for nushell, elvish, xonsh, tcsh, oil and clink. With mise
or `go install`, generate the script with the command.

## What it completes

Completion reads local files only and calls no AWS API:

- `--profile` lists the profiles in `AWS_CONFIG_FILE` or `~/.aws/config`.
- `report inventory --unit` lists the units of the `--index` file on the same command line.
- `--types` lists the resource types tfcensus knows.
- Flags with a fixed set of values, such as `--format`, `--fail-on` and `--layers`, list those
  values with a description in shells that show descriptions.

A lookup that takes more than 1 second offers nothing.

## bash

bash needs the bash-completion package. Copy the script from the archive, or generate it:

```sh
mkdir -p ~/.local/share/bash-completion/completions
tfcensus completion bash > ~/.local/share/bash-completion/completions/tfcensus
```

macOS ships bash 3.2 as `/bin/bash`. It needs brew's `bash-completion` (version 1), not
`bash-completion@2`, which needs bash 4.2 or later. With bash before 4.4, a comma list such as
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

If zsh prints "command not found: compdef", the script loaded before `compinit`. If a new version
still completes the old flags, delete `~/.zcompdump*` and start a new shell. `compaudit` lists
group-writable directories that make `compinit` skip files.

## fish

```sh
tfcensus completion fish > ~/.config/fish/completions/tfcensus.fish
```

Don't add `tfcensus completion fish | source` to `config.fish`: it runs tfcensus on every shell
start. fish ignores the file extension filter, so `--config` lists every file.

## PowerShell

Save the script and dot-source it from your profile:

```powershell
tfcensus completion powershell | Out-File -Encoding utf8 ~/tfcensus.ps1
Add-Content $PROFILE ". ~/tfcensus.ps1"
```

`$PROFILE` is a different file in Windows PowerShell 5.1, in PowerShell 7 on Windows and on macOS
or Linux, so add the line to each shell you use. Descriptions show with
`Set-PSReadLineKeyHandler -Key Tab -Function MenuComplete`. PowerShell also ignores the file
extension filter.

## nushell, elvish, xonsh, tcsh, oil and clink

carapace, which is built into tfcensus, writes these scripts, so nothing else needs installing.
Each script calls tfcensus back to get the completions, so tfcensus must be on `PATH`.

| Shell | Setup |
|---|---|
| nushell | `tfcensus completion nushell \| save --force ~/.config/nushell/tfcensus.nu`, then in `config.nu`: `source ~/.config/nushell/tfcensus.nu` and `$env.config.completions.external = {enable: true, completer: $tfcensus_completer}` |
| elvish | `eval (tfcensus completion elvish \| slurp)` in `~/.config/elvish/rc.elv` |
| xonsh | `exec($(tfcensus completion xonsh))` in `~/.xonshrc` |
| tcsh | ``eval `tfcensus completion tcsh` `` in `~/.tcshrc` |
| oil | `source <(tfcensus completion oil)` in your oils startup file |
| clink | `tfcensus completion clink > %LOCALAPPDATA%\clink\tfcensus.lua` |

The nushell completer handles every external command. If you already have one, call
`$tfcensus_completer` from it when the first span is `tfcensus`.

## Debugging

`tfcensus __complete <args> ''` prints what the shell would offer, followed by `:<directive>`:

```sh
tfcensus __complete scan --format ''
```

`BASH_COMP_DEBUG_FILE=/tmp/comp.log` makes the bash, zsh, fish and PowerShell scripts log each
request to that file.

The [`completion`](/cli/completion) page lists the command for each shell.
