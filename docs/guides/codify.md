---
description: "Turn the unmanaged findings of a scan report into import blocks for one unit, pick findings by type or CEL expression, and generate the config with terraform plan."
---

# Adopt unmanaged resources with codify

`tfcensus codify` reads a scan report and writes `import` blocks for its unmanaged resources into
one unit. It is the first step of bringing a resource under Terraform or OpenTofu: you plan with
`-generate-config-out`, review the config it writes, and move it into your code. codify never runs
plan or apply, and it writes nothing outside the unit's directory.

## Save a scan report

codify reads the JSON that `scan` writes. Narrow the scan to the resources you want to adopt, or
narrow them later with `--types` and `--select`:

```sh
tfcensus scan --profile acme-nonprod --types 'AWS::IAM::*' --format json > reports/scan.json
```

Only findings in the `unmanaged` category are codified. A resource that a rule puts in `ignored`
or `other-iac` is left out, so check the [scan categories](/concepts/categories) first.

## Write the import blocks

Pick the unit that should own the resources with `--into`. The directory must exist:

```sh
tfcensus codify --from reports/scan.json --into live/nonprod/eu-west-1/iam
```

`--from -` reads the report from stdin. codify writes `imports_tfcensus_<YYYYMMDD>.tf`, dated by the
UTC clock, and prints what it wrote and what to run next. This run comes from the test fixtures,
where the unit's directory is `unit` and the command also passes
`--target terraform@1.14.0 --query`, which the sections below explain:

<<< @/snippets/codify.txt

In a Terragrunt unit the next steps use `terragrunt run`, and in a plain Terraform directory they
use `terraform plan`. A block looks like this:

```hcl
import {
  # arn:aws:iam::111111111111:role/clickops-admin
  to = aws_iam_role.clickops_admin_2
  identity = {
    account_id = "111111111111"
    name       = "clickops-admin"
  }
}
```

The comment holds the resource's ARN. Names come from the `Name` tag, then the resource's name,
then the last part of its ID, as lowercase letters, digits and underscores. A name that is taken,
in this run or by a resource or import block already in the directory, gets `_2`, `_3` and so on,
as above. A resource that an import block in the directory already imports is skipped.

codify refuses to overwrite a file it wrote before, so a second run on the same day fails with exit
2 until you pass `--force`.

## Identity or ID imports

Each block imports by identity when the target can, and by ID otherwise:

- Identity needs terraform or OpenTofu 1.12 or later, an aws provider of 6.0 or later in the
  directory's `.terraform.lock.hcl` (or no lock file), and a type whose identity attributes the
  scan gives.
- Otherwise the block has an `id`, built from the type's ID template in the type mapping.
- A finding with neither is listed as skipped, with the reason.

codify finds the engine from the lock file's registry (`registry.opentofu.org` means OpenTofu),
else from the name of the `--tf-path` binary, and the version from `<tf-path> version -json`.
`--target` skips the detection, and `--import-by id` writes ID imports for every block:

```sh
tfcensus codify --from reports/scan.json --into live/nonprod/eu-west-1/iam --target opentofu@1.11.0
tfcensus codify --from reports/scan.json --into live/nonprod/eu-west-1/iam --import-by id --force
```

When detection fails, codify writes ID imports.

## Pick the findings

`--types` takes CloudFormation or Terraform type globs. A Terraform type also picks which resource
type the block uses, for a CloudFormation type that more than one Terraform type maps to:

```sh
tfcensus codify --from reports/scan.json --into live/nonprod/eu-west-1/network --types 'aws_security_group'
```

`--select` takes a CEL expression over `resource`, the variable ownership rules use, and codifies
the findings it is true for:

```sh
tfcensus codify --from reports/scan.json --into live/nonprod/iam --select 'resource.tags.?team.orValue("") == "platform"'
tfcensus codify --from reports/scan.json --into live/nonprod/iam --select 'resource.name.startsWith("clickops-")'
```

The report holds the account, region, type, ARN, ID, name, tags, the layers and the creation time
of each finding, so those fields work. Attributes, parents and relationships are empty. Read tags
with `.?key.orValue("")`: an expression that fails on one finding, such as `resource.tags["team"]`
on an untagged role, stops codify with exit 2. The [CEL reference](/reference/cel) lists the fields
and functions.

## List blocks for terraform query

`--query` also writes `query_tfcensus_<YYYYMMDD>.tfquery.hcl`, with one `list` block per type and
region, for the types that have a provider list resource without required list config.
`terraform query -generate-config-out` turns it into config for every resource of those types,
managed or not. It needs terraform 1.14 or later. OpenTofu has no query support yet, so codify
writes no query file for an OpenTofu target.

```sh
tfcensus codify --from reports/scan.json --into live/nonprod/iam --target terraform@1.14.0 --query
```

## Finish the adoption

1. Run the plan from the next steps. It writes `generated.tf` with the config of every imported
   resource.
2. Review the generated config. It holds every attribute the provider read, including defaults
   you may not want to pin, and sometimes values that don't belong in code.
3. Move the resources into your modules and delete the import file once the imports are applied.
4. Plan until it shows no changes, then apply.

Run the scan again afterwards: the resources now show as managed. When a resource belongs in
another unit after all, [move it between units](/guides/move).
