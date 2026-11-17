---
description: The terragrunt, terraform and OpenTofu versions tfcensus runs with, the features that need newer ones, and the state versions it reads.
---

# Supported versions

tfcensus runs terragrunt and terraform or OpenTofu in your units for discovery, `drift`,
`move` and `codify`. It reads state and plan files itself. The table lists the oldest version of
each tool it runs, the versions its end-to-end tests use, and the features that need a newer one.

<!--@include: ./parts/supported-versions.md-->

`tfcensus doctor` prints the version of each tool it finds, with the features that version is too
old for, and exits 1 when a tool is older than its minimum. A tool that isn't installed is reported
and doesn't fail the run, because commands such as `scan` and `audit backend` don't run it.

The features in the table:

- Resource identity: `codify` writes import blocks by identity on terraform or OpenTofu 1.12 or
  later, and by ID on older versions.
- `query`: `codify --query` writes list blocks for `terraform query`, which OpenTofu doesn't have.
- `use_lockfile`: `audit backend` reports units that still lock with DynamoDB only (LK-MODE).

## Tools

- terragrunt 1.0 and later promise no breaking changes, so tfcensus supports every 1.x release.
  It doesn't support terragrunt before 1.0.
- terragrunt runs OpenTofu unless it's told otherwise. tfcensus passes the binary from `--tf-path`
  (config `binaries.terraform`, default `terraform`) on every call, so set it to `tofu` for an
  OpenTofu fleet. `--tg-path` sets the terragrunt binary.
- `doctor` tells terraform and OpenTofu apart by the first line of `version` output, so a
  `terraform` name that points to OpenTofu is still reported as OpenTofu.

## State and plan files

Reading state doesn't depend on the tool version. tfcensus reads every state of format version 4,
which terraform 0.12 and later and every OpenTofu release write. A state of an older format shows
as `unsupported` in `inventory`, and the other states are still read. Plan JSON is read by its
`format_version` major, 1.x today, and unknown fields are ignored.

OpenTofu states with client-side encryption show as `encrypted`, with only serial and lineage.
`inventory --decrypt-with-tofu` reads them through `tofu show -json` in the unit, which needs
OpenTofu 1.7 or later.

## The AWS provider

tfcensus doesn't run the AWS provider. Its type mapping comes from the provider source
([type coverage](/reference/type-coverage)). Matching by resource identity works for states that hold
identity data, which terraform and OpenTofu 1.12 or later write with AWS provider 6.0 or later.
Other states match by ARN and ID.
