---
description: The migration file that tfcensus move runs, every attribute of a migration block, the address and action syntax, the history object, and the findings that reject a block.
---

# Move file format

`tfcensus move plan|apply|rollback|status|mark-applied <file>` runs a migration file: HCL with one
or more `migration "units"` blocks. Each block moves resources from one unit's state into others,
or changes addresses inside one state. `tfcensus move suggest` drafts the file from plans.

```hcl
migration "units" "split_network" {
  from = "live/prod/eu-west-1/network"
  to = {
    "live/prod/eu-west-1/network-sg"        = ["aws_security_group.app", "aws_vpc_security_group_ingress_rule.app[*]"]
    "live/prod/eu-west-1/network-endpoints" = ["module.endpoints"]
  }
}

migration "units" "rename_in_place" {
  from    = "live/prod/eu-west-1/eks"
  actions = ["mv module.eks_old module.eks", "rm aws_iam_role.legacy"]
}
```

Blocks run in file order, and a later block sees the result of an earlier one. `--block` runs only
the named blocks. Block names are unique in a file and use letters, digits, `_` and `-`, starting
with a letter or `_`. Only the block type `units` exists.

## Attributes

| Attribute | Required | Value |
|---|---|---|
| `from` | yes | The source unit. |
| `to` | one of `to` and `actions` | A map of destination unit to the list of addresses that move there. |
| `actions` | one of `to` and `actions` | A list of tfmigrate-style actions that run on the state of `from`. |
| `strategy` | no | `surgery` (default) pushes the changed states. `native` writes `import` and `removed` blocks for your own apply flow instead, and takes `to` only. |

## Unit paths

A unit path is relative to the repository root, which `--repo` sets (default the current
directory). It must stay inside the repository. A path names the unit's default workspace; append
`:<workspace>` for another workspace of the unit's s3 backend, such as `live/network:blue`.
`live/network:default` is the same as `live/network`. The workspace must have a state already.
The `native` strategy works on the default workspace only, because its files are part of the
configuration that every workspace applies.

## Addresses in `to`

| Address | Selects |
|---|---|
| `module.endpoints` | every resource in the module and its child modules. A module step without a key matches every instance of the module call. |
| `aws_route53_record.api` | every instance of the resource |
| `aws_route53_record.api[0]`, `aws_s3_bucket.logs["eu"]` | one instance |
| `aws_vpc_security_group_ingress_rule.app[*]` | every instance that the pattern matches. A pattern with `*` must match a whole instance address. |

Every address must match at least one instance. Addresses stay the same in the destination, and
a destination that already has an instance at the address stops the block. Data sources stay in
the source, because terraform reads them again.

## Actions

Each action is split like a shell command, so quote addresses that hold spaces or `for_each` keys.

| Action | Effect |
|---|---|
| `mv <src> <dst>` | moves a module, a whole resource or one instance |
| `xmv <src> <dst>` | expands each `*` in the source against every address in the state. `$1`, `$2` and so on fill the destination; inside an HCL string, write `$${1}` for the braced form. |
| `rm <addr>...` | removes everything under each address from the state |

```hcl
actions = [
  "xmv aws_security_group_rule.app[*] aws_vpc_security_group_ingress_rule.app[$1]",
  "mv 'aws_s3_bucket.logs[\"eu west\"]' aws_s3_bucket.logs_eu",
]
```

## Variables

`var.NAME` comes from `--var NAME=VALUE` and `env.NAME` from the environment, so one file can serve
several environments:

```hcl
migration "units" "split_network" {
  from = "live/${var.env}/network"
  to   = { "live/${var.env}/network-sg" = ["aws_security_group.app"] }
}
```

## History

Every unit's history is the object `<state key>.tfcensus-migrations.json` next to its state.
`move apply` and `move mark-applied` add a record per block, `move status` reads them, and
`move rollback` uses the S3 versions they hold:

```json
{
  "version": 1,
  "unit": "live/prod/eu-west-1/network",
  "records": [
    {
      "file": "2026-11-split-network.hcl",
      "block": "split_network",
      "role": "source",
      "strategy": "surgery",
      "applied_at": "2026-11-12T09:00:00Z",
      "by": "arn:aws:iam::111111111111:role/platform-admin",
      "version_ids": { "before": "3HL4kqtJ...", "after": "7b2fQe1M..." }
    }
  ]
}
```

`file` is the migration file's base name, so renaming the file makes its blocks pending again.
History is kept per unit, so applying a file with `--var env=staging` doesn't mark it applied for
`--var env=prod`. `version_ids.before` is empty when the unit had no state before the push.

## Findings

`move plan` and `move apply` check each block before anything is written. A block with a finding
is rejected: `move plan` reports it, `move apply` stops before the block, and both exit 3.

<!--@include: ../parts/move-rules.md-->

The permissions each move command needs are on the [IAM page](/reference/iam#move-permissions).
