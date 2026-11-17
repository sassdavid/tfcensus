---
description: How to report a vulnerability in tfcensus, how to verify a release's signature and provenance, and where the third-party license notices are.
---

# Security and verification

This page covers reporting a vulnerability and checking that a release came from this repository.
What tfcensus reads, writes and prints is on the [safety model](/concepts/safety) page.

## Report a vulnerability

Report it privately through GitHub: open the
[security advisory form](https://github.com/sassdavid/tfcensus/security/advisories/new) on the
repository. Don't open a public issue for a vulnerability. Include the version
(`tfcensus version`), the command you ran, and what it printed, with account IDs, ARNs and other
identifiers replaced.

A fix ships in a new release, and the [changelog](/changelog) lists it.

## What the project checks

Every pull request and push to `main` runs:

- `govulncheck` on the reachable code, with the results in GitHub code scanning. It also runs
  weekly.
- zizmor on the GitHub Actions workflows.
- GitHub's dependency review on every pull request.
- A license check that fails on forbidden, restricted or unknown licenses in any dependency.

Dependencies and actions are pinned to exact versions and commit SHAs. Renovate proposes updates
once a release is at least 3 days old.

## Verify a release

Each release has a `checksums.txt` signed with cosign (keyless, through GitHub's OIDC token), and a
GitHub build provenance attestation for every archive. Check the signature, then the archive's
checksum and provenance:

```sh
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/sassdavid/tfcensus/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
sha256sum --ignore-missing -c checksums.txt
gh attestation verify --owner sassdavid tfcensus_<version>_<os>_<arch>.tar.gz
```

On macOS, use `shasum -a 256 --ignore-missing -c checksums.txt` for the checksum. The signature
proves the checksums came from the release workflow of this repository. The attestation proves the
archive was built by it.

The release also has an SPDX SBOM for each archive. A binary from `go install` is built on your
machine and has no signature or attestation.

## Third-party notices

Every archive holds a `third_party_licenses/` directory with the license of each Go module
compiled into the binary, for every release platform. `THIRD_PARTY_NOTICES.md` in that directory
indexes them and links each module's source on the Go module mirror. Data that tfcensus embeds from
other projects, such as the type mapping derived from the AWS providers, keeps its license notice
in the same directory.

tfcensus itself is under the [Apache-2.0 license](https://github.com/sassdavid/tfcensus/blob/main/LICENSE).
