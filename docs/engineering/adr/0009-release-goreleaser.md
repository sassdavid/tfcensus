# ADR-0009: release-please + goreleaser v2, signed and attested

- Status: accepted (2026-11-09)

## Decision
- **release-please** maintains a release PR (version bump + `CHANGELOG.md` from Conventional Commits). Merging it creates the tag.
- **goreleaser v2.20.0** runs on the tag:
  - `CGO_ENABLED=0` cross builds
  - checksums
  - syft SBOMs
  - cosign v3 keyless `sign-blob --bundle`
  - `actions/attest` build provenance
  - `homebrew_casks`
  - `dockers_v2` (CI container image)
- Distribution order: GitHub Releases → Homebrew tap → `mise use github:sassdavid/tfcensus` → aqua-registry PR → mise registry PR. Details in [ci-and-release.md](../ci-and-release.md).

## Reasons
- This is exactly the pattern goreleaser, gh and aqua use.
- Keyless signing needs no key management.
- Attestations let `gh attestation verify` work.
- The aqua registry and mise's `github:` backend verify checksums, and aqua can verify the cosign bundle and SLSA provenance. Your own mise config has `locked_verify_provenance = true`, so publishing provenance lets you lock these tools the same way as everything else.

## Alternatives rejected
- cargo-dist: Rust only.
- Hand-written workflow: reinvents goreleaser.
- `brews:`: deprecated in favour of `homebrew_casks:`.
