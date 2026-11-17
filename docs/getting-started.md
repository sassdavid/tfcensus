---
description: "A first read-only run of tfcensus: check the setup with doctor, list units, read their state with inventory, and find unmanaged AWS resources with scan."
---

# Getting started

This page takes you from an installed binary to a first scan, in four read-only commands. None of
them changes anything in AWS or in your repositories.

The outputs below come from tfcensus's own test fixtures, so they match what this release prints.
The fixtures are sample repositories named `tg-live`, `tf-fleet` and `tf-scan`, and the AWS account
`111111111111` behind the profile `acme-nonprod`. Use your own paths and profiles.

## Before you start

- tfcensus is [installed](/install).
- An AWS profile can read the accounts you want to look at. The [read-only policy](/reference/iam)
  has every action tfcensus calls.
- terragrunt and terraform (or OpenTofu) are on `PATH` if your repository uses Terragrunt. The
  [supported versions](/supported-versions) page lists the oldest versions tfcensus runs.

## 1. Check the setup

`doctor` checks the tool versions, loads the credentials of each profile, and probes each account
and region for the inventory sources a scan can use:

```sh
tfcensus doctor --profile acme-nonprod
```

<<< @/snippets/doctor.txt

The first table lists terragrunt and terraform. Here neither is on `PATH`, which `doctor` reports
without failing. A plain Terraform repository needs neither for `inventory` and `scan`, and a
Terragrunt repository needs terragrunt to list its units. The second table
says the profile loaded and which account it belongs to. The third shows, per region, whether
Resource Explorer and AWS Config are set up, and the layers a scan uses there: `re` (Resource
Explorer), `config` (AWS Config), `cc` (Cloud Control) and `native` (tfcensus's own listers). The
notes at the end say what an admin could turn on. `doctor` never turns anything on itself.

## 2. List the units

`units` finds the Terragrunt units and plain Terraform root modules under a path, and shows where
each keeps its state and which account and region it manages:

```sh
tfcensus units tg-live
```

<<< @/../testdata/golden/units/tg-live.txt

`fast-path` means tfcensus evaluated the unit's `remote_state` itself. The `dns` unit calls
`run_cmd`, which only terragrunt can evaluate, so tfcensus rendered it with terragrunt (`render`).
The `secrets` unit failed both ways, and the notes under the table say why. The
`terragrunt-stack` row is a stack whose units aren't generated yet: `--generate-stacks` lists them.

## 3. Read the state

`inventory` reads each unit's state from its S3 backend and counts what it manages. It takes no
lock, so it is safe to run during an apply, and it prints identifiers and counts, never attribute
values:

```sh
tfcensus inventory tf-fleet
```

<<< @/../testdata/golden/inventory/tf-fleet.txt

Each workspace of a unit is its own row. The rows that aren't `ok` show what can go wrong: a unit
that has no state yet (`absent`), a state written by Terraform before 0.12 (`unsupported`), a
state the backend credentials can't read (`error`), and an OpenTofu state with client-side
encryption (`encrypted`). Each problem is printed under the table with what to do about it. The
`double-managed` line says that two states manage the same security group, which a later apply
of one of them would fight over.

## 4. Find unmanaged resources

`scan` lists the live resources of each account and region, matches them against the states of
the units, and puts each resource in a category:

```sh
tfcensus scan tf-scan --config tf-scan/tfcensus.yaml
```

<<< @/snippets/scan.txt

The table counts each category per account and region:

- `managed`: one state manages the resource. `double-managed`: more than one does.
- `unmanaged`: no state manages it and no ownership rule explains it. These are the findings to
  look at: click-ops, leftovers and resources someone forgot to import.
- `other-iac`, `aws-managed`, `derived` and `ignored`: no state manages it, and an ownership rule
  says why, such as a CloudFormation stack, the default VPC or a child that AWS created with its
  parent. The [rule packs](/reference/rule-packs) list the built-in rules.
- `undetermined`: tfcensus has no mapping for the type, so it can't tell. The
  [type coverage](/reference/type-coverage) page lists the mapped types.

Coverage is the share of managed resources among managed, double-managed and unmanaged ones. Every
finding carries a `rule` and a `reason`, which `--format json` prints, and the
[rule IDs](/reference/rule-ids) page explains each rule.

## Next steps

- Gate CI on new unmanaged resources with `tfcensus scan --fail-on unmanaged`, which exits 3 when
  it finds one ([exit codes](/reference/exit-codes)).
- Check the state buckets and lock tables with `tfcensus audit backend`
  ([audit checks](/reference/audit-checks)).
- Put your profiles, regions and rules in a config file: `tfcensus config init` writes a
  commented file to start from ([configuration](/reference/config)).
- Every command, flag and output format is in the [CLI reference](/cli/).
