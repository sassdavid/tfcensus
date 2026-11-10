# tfcensus

tfcensus takes a census of Terraform/OpenTofu and Terragrunt fleets on AWS. It lists units and the
resources in their state, finds AWS resources that no unit manages, and audits state backends.
Drift, whois/codify and cross-unit moves come later. It is read-only by default.

tfcensus is pre-release. Milestones M0 to M3 are in place: `units` lists units and their
backends, `doctor` checks credentials and inventory layers, and `inventory` reads each unit's state
from S3 and prints identifiers and counts, never attribute values. The plan is in [docs/plan](docs/plan/) and the CLI reference in
[docs/cli](docs/cli/tfcensus.md).

## Install

Once v0.1.0 is out:

```sh
brew install --cask sassdavid/tap/tfcensus
scoop bucket add sassdavid https://github.com/sassdavid/scoop-bucket && scoop install tfcensus
mise use github:sassdavid/tfcensus
docker run --rm ghcr.io/sassdavid/tfcensus:latest version
go install github.com/sassdavid/tfcensus/cmd/tfcensus@latest   # unsigned
```

Release checksums are signed with cosign (keyless) and carry GitHub build-provenance attestations:

```sh
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/sassdavid/tfcensus/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
gh attestation verify --owner sassdavid tfcensus_<version>_<os>_<arch>.tar.gz
```

The read-only IAM policy tfcensus needs is in [docs/iam](docs/iam/).

## Configuration

`tfcensus config show --sources` prints every effective setting and where it came from.
Layers, lowest to highest: defaults, `~/.config/tfcensus/config.yaml`, `.tfcensus.yaml` (nearest, up
to the git root), `TFCENSUS_*` env vars, flags. Unknown keys are errors. Schema:
[schemas/config-v1.json](schemas/config-v1.json). The env var names are in
[docs/cli/tfcensus_config.md](docs/cli/tfcensus_config.md).

## Development

```sh
mise install                         # pinned toolchain (mise.toml)
mise run check                       # linters (Go, prose, workflows) + go test -race + govulncheck
mise run test:golden -- -update      # regenerate golden files, then review the diff
mise run docs                        # regenerate docs/cli
mise run snapshot                    # local goreleaser build into dist/
mise run sibling:diff                # compare the files shared with relgap
```

## License

[Apache-2.0](LICENSE). Each release archive and the container image (under
`/usr/share/doc/tfcensus/`) have a `third_party_licenses/` directory with the notices of everything
compiled into the binary and an index that links each module's source.
