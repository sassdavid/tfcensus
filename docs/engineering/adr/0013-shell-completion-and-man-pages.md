# ADR-0013: Shell completion, embedded carapace and man pages

- Status: accepted (2026-11-13)

## Context

Up to v0.1.0, tfcensus had cobra's default `completion bash|zsh|fish|powershell` and no
completion functions, so enum flags such as `--format` fell back to file names. The archives
carried no completion scripts or man pages. cobra can't generate nushell, elvish, xonsh, tcsh,
oil or clink scripts (spf13/cobra#2364). The research is in
[roadmap-research.md](../../plan/roadmap-research.md#shell-completion).

## Decision

- bash, zsh, fish and PowerShell use cobra's scripts. Every flag that takes a value and every
  positional argument gets a completion function from `internal/complete`, the package both CLIs
  share. A `Choice` list feeds the help text (`complete.Usage`) and the completion
  (`complete.Enum`, `complete.List`), the way gh's `StringEnumFlag` does. Files and directories
  use `complete.Files` and `complete.Dirs`. These are completion functions, not cobra
  annotations, because carapace reads only the functions.
- Dynamic completion reads local files only: profile names from `AWS_CONFIG_FILE` or
  `~/.aws/config` (never the credentials file), unit paths from the `--index` file on the command
  line, and types from the embedded typemap. `complete.Dynamic` gives up after 1 s and returns
  `ShellCompDirectiveError` with no results. A `go list -deps` test fails if `internal/complete`
  links the AWS SDK, `execx`, `httpx`, `net/http` or `os/exec`.
- `__complete` and `_carapace` skip the root `PersistentPreRunE`, so they open no log file and
  print nothing but results.
- carapace v1.18.1 (Apache-2.0) is embedded. `tfcensus completion nushell|elvish|xonsh|tcsh|oil|clink`
  prints carapace's snippet, which calls the hidden `tfcensus _carapace` command back, so users
  install nothing else. `carapace.Gen` appends to cobra's `OnInitialize` slice and fills a global
  map, so only `cli.Main` (the process entry point) calls it. In-process tests call `cli.Execute`.
  carapace's `LICENSE.appendix` reserves its `Uid`, `Query` and `Trace` features for Tabdance;
  tfcensus calls none of them.
- `scripts/completions.sh`, a goreleaser before hook, writes `completions/` (bash, zsh, fish,
  ps1) and `manpages/*.1.gz`. The man pages come from cobra/doc `GenManTree` in
  `scripts/gendocs -man`, so go-md2man stays out of the release binary. Pages are dated from
  `SOURCE_DATE_EPOCH` (else the last commit) and gzipped without a name or time, so a rerun writes
  the same bytes.
- Every archive carries both directories. The Homebrew cask installs them with goreleaser's
  `completions:` and `manpages:` keys instead of `generate_completions_from_executable`. Scoop has
  no completion stanza, so `post_install` prints the `$PROFILE` line with the `current` path.
- The `completion-shells` CI job loads the scripts in bash 5, bash 3.2, zsh, fish, nushell, pwsh
  on three OSes and Windows PowerShell 5.1 (`scripts/completion-shells.sh` and `.ps1`).

## Consequences

- Two new modules (carapace, carapace-shlex) and about 1 MB more binary.
- carapace has one main maintainer and frequent releases; Renovate's minimum release age applies.
  A bump changes the `_carapace export` output, which `testdata/script/complete.txtar` matches.
- fish and PowerShell ignore the file extension filter and list every file.
- Not adopted: the `usage` spec (its cobra integration is a preview, it skips dynamic completion
  functions, and its scripts need the `usage` binary).
