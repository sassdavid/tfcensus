---
description: Install tfcensus from a release archive, with mise or with go install, verify the release signature, and install the man pages and completion scripts.
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

The archive holds `completions/tfcensus.bash`, `.zsh`, `.fish` and `.ps1`, and
`tfcensus completion <shell>` prints the scripts for more shells.
[Shell completion](/guides/completion) sets them up for each shell.
