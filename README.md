# tfcensus

tfcensus takes a census of Terraform/OpenTofu and Terragrunt fleets on AWS. It lists units and the
resources in their state, finds AWS resources that no unit manages, audits state backends and
reports drift. It tells which unit manages a resource, writes import blocks for the ones no unit
manages, and moves resources between units. It is read-only by default: only `move apply`,
`move rollback` and `move mark-applied` write.

tfcensus is pre-release. Milestones M0 to M10 are in place: `units` lists units and their
backends, `doctor` checks credentials and inventory layers, `inventory` reads each unit's state
from S3 and prints identifiers and counts, never attribute values, and `audit backend` checks state
buckets, lock tables and the fleet of states, with SARIF output. `scan` finds AWS resources that no
unit manages, and `drift` runs a refresh-only plan in every unit and groups identical drift across
units ([docs/ci/drift.yml](docs/ci/drift.yml) is an example workflow). `whois` names the units
that manage an ARN or id, offline from a saved index too, and `codify` turns a scan report into
import blocks for one unit. `move` runs migration files that move resources between the states of
units, in other accounts too, verified with plans and rolled back from S3 object versions
([docs/runbooks/move.md](docs/runbooks/move.md)). `notify` publishes saved scan, drift and audit
reports to Slack, GitHub issues, Bitbucket pull request comments or files
([docs/runbooks/notify.md](docs/runbooks/notify.md)), `report` renders them offline, and `mcp`
serves them read-only to agents such as Claude Code. The plan is in
[docs/plan](docs/plan/) and the CLI reference in [docs/cli](docs/cli/tfcensus.md).

## Install

Releases are on [GitHub](https://github.com/sassdavid/tfcensus/releases): download the archive for
your platform, or let mise do it.

```sh
mise use github:sassdavid/tfcensus
go install github.com/sassdavid/tfcensus/cmd/tfcensus@latest   # unsigned
```

Homebrew, Scoop and the ghcr.io image come later (milestone M19). Each archive carries shell
completion scripts and man pages; [docs/install.md](docs/install.md) sets them up for each shell.

Release checksums are signed with cosign (keyless) and carry GitHub build-provenance attestations:

```sh
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/sassdavid/tfcensus/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
gh attestation verify --owner sassdavid tfcensus_<version>_<os>_<arch>.tar.gz
```

The read-only IAM policy tfcensus needs is in [docs/iam](docs/iam/).

## HTML report

`tfcensus report html` turns the JSON that `scan`, `drift` and `audit` print with `--format json`
into one HTML file. The CSS and JavaScript are inside the file, and its Content-Security-Policy
blocks every network request, so it opens offline and can be kept as a CI artifact.

```sh
tfcensus scan --format json > scan.json
tfcensus drift --format json > drift.json
tfcensus audit backend --format json > audit.json
tfcensus report html --from scan.json --from drift.json --from audit.json -o census.html
```

The scan section has per-account counts and a findings table you can filter by account, category
and type. Pass two or more scan files from different days to get a coverage trend. Drift shows
attribute paths, never values, even when the drift JSON was written with `--show-attributes`.
The same input files always give the same bytes, so the file can be compared or committed.

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
