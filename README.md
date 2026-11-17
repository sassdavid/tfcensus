# tfcensus

tfcensus takes a census of Terraform/OpenTofu and Terragrunt fleets on AWS. It lists units and the
resources in their state, finds AWS resources that no unit manages, audits state backends and
reports drift. It tells which unit manages a resource, writes import blocks for the ones no unit
manages, and moves resources between units. It is read-only by default: only `move apply`,
`move rollback` and `move mark-applied` write.

tfcensus is pre-release, and its commands and output can still change before 1.0.

The documentation is at [tfcensus.sasskovacs.dev](https://tfcensus.sasskovacs.dev).

## Install

Releases are on [GitHub](https://github.com/sassdavid/tfcensus/releases): download the archive for
your platform, or let mise do it.

```sh
mise use github:sassdavid/tfcensus
go install github.com/sassdavid/tfcensus/cmd/tfcensus@latest   # unsigned
```

Homebrew, Scoop and the ghcr.io image are not published yet. The
[install page](https://tfcensus.sasskovacs.dev/install) sets up completion and man pages, and
[security and verification](https://tfcensus.sasskovacs.dev/security) checks a release's signature
and provenance.

## Links

- [Getting started](https://tfcensus.sasskovacs.dev/getting-started): a first read-only run with
  `doctor`, `units`, `inventory` and `scan`.
- [Guides](https://tfcensus.sasskovacs.dev/guides/scan): scans, ownership rules, backend audits,
  drift in CI, `whois`, `codify`, moves, `notify`, the HTML report and the MCP server.
- [Read-only by default](https://tfcensus.sasskovacs.dev/concepts/safety): what tfcensus reads,
  writes and prints.
- [CLI reference](https://tfcensus.sasskovacs.dev/cli/) and
  [configuration](https://tfcensus.sasskovacs.dev/reference/config).
- [IAM policy](https://tfcensus.sasskovacs.dev/reference/iam) for the read-only role.
- [tfcensus-action](https://github.com/sassdavid/tfcensus-action) runs tfcensus in GitHub Actions.
- [Contributing](https://tfcensus.sasskovacs.dev/contributing) and the
  [changelog](CHANGELOG.md).

## License

[Apache-2.0](LICENSE). Each release archive and the container image (under
`/usr/share/doc/tfcensus/`) have a `third_party_licenses/` directory with the notices of everything
compiled into the binary and an index that links each module's source.
