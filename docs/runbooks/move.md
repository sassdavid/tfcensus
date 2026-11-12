# Move resources between Terragrunt units

This runbook moves resources from one unit's state to other units' states, in the same account or
in another one, with `tfcensus move`. It also covers renames inside one state, rollback and the
native strategy for teams that apply only through pull requests.

For a refactor inside a single plain Terraform directory, tfmigrate or `moved` blocks are the
simpler tools. Use `tfcensus move` when resources cross unit boundaries.

## How it works

You write a migration file. Each `migration "units"` block names a source unit and either the
destination units with the addresses that go to each, or tfmigrate-style actions to run inside
the source.

- `move plan` reads each involved state from S3, applies the block in memory and runs a plan in
  every unit against its new state. A block passes when every resource change is `no-op` or
  `read`. Output changes don't count.
- `move apply` does the same, asks you to confirm, then pushes the destinations and then the
  source with `terragrunt run -- state push`. It records the S3 version of each state from before
  the push in a history object next to the state.
- `move rollback` copies those versions back, byte for byte.

`plan` and `status` only read. `apply`, `rollback` and `mark-applied` are the only tfcensus
commands that write to a backend, and none of them is available over MCP.

## Before you start

Check each unit the migration touches:

1. It has an `s3` backend that `tfcensus units` resolves (bucket, key and region shown). Only the
   default workspace can be moved.
2. Its terragrunt `remote_state` uses a `generate` block. Verification puts a local backend in
   front of the unit through `_tfcensus_override.tf`, and that only works with a generated backend.
3. Its state bucket has versioning on. `tfcensus audit backend` reports it as S3-VER. Without it,
   apply refuses (MV-VERSIONING) because a push couldn't be rolled back.
4. Nobody else applies the unit while you work. Pause the CI or Atlantis jobs for these units.
   tfcensus checks that no state changed between its read and its push and stops if one did, but
   it can't stop a run that starts after the push.

Your credentials need, for every involved backend:

| Command | Permissions |
|---|---|
| `plan`, `status` | `s3:GetObject`, `s3:GetBucketVersioning`, plus what `terragrunt plan` needs in the unit |
| `apply` | the above, `s3:GetObjectVersion`, and what `terraform state push` needs: `s3:PutObject` on the state key, plus the lock (`s3:PutObject` and `s3:DeleteObject` on `<key>.tflock`, or `dynamodb:PutItem`, `dynamodb:GetItem` and `dynamodb:DeleteItem`) and `s3:PutObject` on `<key>.tfcensus-migrations.json` |
| `rollback` | the `apply` set, plus `s3:DeleteObject` on the state key |
| `mark-applied` | `s3:GetObject` and `s3:PutObject` on `<key>.tfcensus-migrations.json` |

tfcensus reads each unit with that unit's own backend credentials: the backend's `profile`, or its
`role_arn` assumed from the profile `accounts.map` names for the bucket's account, or the default
credentials. Terragrunt runs in each unit with the unit's own configuration. No single call holds
two accounts' credentials.

Set the terraform binary once, because terragrunt defaults to `tofu`: pass `--tf-path terraform`
or set `binaries.terraform` in `.tfcensus.yaml`.

## Write the migration file

Put the file in the repository, for example `migrations/2026-11-split-network.hcl`. Unit paths are
relative to the repository root (`--repo`, default the current directory).

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

Blocks run in file order, and a later block sees the result of an earlier one. Addresses stay the
same in the destination.

A `to` entry selects instances in one of these ways:

- `module.endpoints` selects every resource in the module and its child modules. A module step
  without a key, such as `module.svc`, matches every instance of the module call.
- `aws_route53_record.api` selects every instance of the resource.
- `aws_route53_record.api[0]` or `aws_s3_bucket.logs["eu"]` selects one instance.
- A pattern with `*` must match a whole instance address: `aws_vpc_security_group_ingress_rule.app[*]`.

Every entry must match at least one instance, and data sources stay in the source because they
are read again. A destination that already has an instance at the same address stops the block.

`actions` use tfmigrate's syntax. Each action is split like a shell command, so quote addresses
with spaces or for_each keys:

- `mv <src> <dst>` moves a module, a whole resource or one instance.
- `xmv <src> <dst>` expands each `*` in the source against every address in the state. `$1`,
  `$2` and so on fill the destination. Inside the HCL string, write `$${1}` for the braced form.
- `rm <addr>...` removes everything under each address.

```hcl
actions = [
  "xmv aws_security_group_rule.app[*] aws_vpc_security_group_ingress_rule.app[$1]",
  "mv 'aws_s3_bucket.logs[\"eu west\"]' aws_s3_bucket.logs_eu",
]
```

One file can serve several environments. `var.NAME` comes from `--var NAME=VALUE` and `env.NAME`
from the environment:

```hcl
migration "units" "split_network" {
  from = "live/${var.env}/network"
  to   = { "live/${var.env}/network-sg" = ["aws_security_group.app"] }
}
```

History is kept per unit, so applying this file with `--var env=staging` doesn't mark it applied
for `--var env=prod`.

## Run the move

1. Change the code first, in a branch. Move each resource's configuration into its destination
   unit and remove it from the source, then add the migration file. Don't merge yet.
2. Plan on that branch. Each unit is planned with the branch's configuration:

   ```sh
   tfcensus move plan migrations/2026-11-split-network.hcl --tf-path terraform
   ```

   The output lists each unit's role, how many addresses move in or out, and the serial change. A
   new destination state shows `(new state)`. Each block ends `verified` or `rejected`, with the
   findings that rejected it. The exit code is 3 when a block is rejected.

3. Read every finding before you go on:

   | Rule | Meaning | Fix |
   |---|---|---|
   | MV-VERIFY | A plan against the moved state would create, update, replace or delete a resource. A create in the destination often means its provider points at another account, so refresh found nothing. | Fix the configuration or the provider, then plan again. |
   | MV-PLAN | A plan failed. Terraform's output stays out of the report, because it can hold attribute values. | Run `terragrunt plan` in the unit to see why. |
   | MV-ACCOUNT | A moved resource's ARN names another account than the destination deploys to. | Move it to a unit in its own account. |
   | MV-VERSIONING | The bucket has no versioning. | Turn versioning on. |

4. Apply. tfcensus plans again, then asks you to type the block's last destination unit path (the
   `from` path for an in-place block):

   ```sh
   tfcensus move apply migrations/2026-11-split-network.hcl --tf-path terraform
   ```

   In CI, pass `--yes`. Without a terminal and without `--yes`, apply exits 2 and changes nothing.
   Each push is logged at info level with the unit and its `s3://` location.

5. Check the result. Plan each unit through your normal flow; every plan should show no changes.
   `tfcensus move status <file>` shows the block as applied in every unit.
6. Merge the branch and resume the paused jobs.

Applying the same file again skips blocks that are already applied.

## Roll back

```sh
tfcensus move rollback migrations/2026-11-split-network.hcl --tf-path terraform
```

Rollback works on the newest block first, and within a block on the source before the
destinations. For each unit it takes the backend's lock, copies the version recorded before the
push back over the current state, fixes the DynamoDB digest, and releases the lock. A destination
that had no state before is deleted again. The history records are marked as rolled back, so the
block shows as pending and can be applied again.

Rollback stops when a state changed after the push, because restoring it would drop that change.
Look at what changed (the S3 object versions show it), then pass `--force` if the old version is
still what you want. Revert the code branch as well, so the configuration matches the states.

## When an apply stops halfway

If a push fails after some units were pushed, apply prints the units already pushed. Resources are
then in both the destination and the source states. `move status` shows the block as `partial`,
and apply refuses to run it again. Two ways out:

- Run `move rollback` for the block. It restores the units that have a record.
- Finish the remaining pushes by hand, then run `move mark-applied` so the history matches.

A stale read stops apply before the first push: every unit's current S3 version is compared with
the one tfcensus read, so a run that changed a state in between doesn't leave a half-done block.

## Native strategy for pull-request flows

Teams that change state only through reviewed applies (Atlantis, Digger, CI) can set
`strategy = "native"` on a block. tfcensus then writes files instead of pushing states:

- each destination gets `moves_tfcensus_<block>.tf` with one `import` block per moved instance, by
  identity when the state has one (terraform or OpenTofu 1.12 and later), else by `id`;
- the source gets the same file name with `removed` blocks and `destroy = false`.

`move plan` lists the files. `move apply` writes them (`--force` overwrites earlier ones) and plans
each unit: destinations may only import with no changes, and the source may only forget. Commit the
files with the configuration change, let your normal flow apply them, delete the files afterwards,
and run `tfcensus move mark-applied <file>` to record the migration.

Check the `id` imports: for some resource types the import ID isn't the `id` attribute, and the
plan then fails the import. A move of only some instances of a resource, or of resources inside a
module instance with a key, can't be written as `removed` blocks and needs surgery.

## Migrations done by hand

`tfcensus move mark-applied <file>` writes a history record in every unit of each block without
touching any state. tfcensus can't roll back such records; restore those states by hand.

## History

Each unit's history is `<state key>.tfcensus-migrations.json` in the state bucket:

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
      "version_ids": {"before": "3HL4kqtJ...", "after": "7b2fQe1M..."}
    }
  ]
}
```

`file` is the migration file's base name, so renaming the file makes its blocks pending again.
`version_ids.before` is empty when the unit had no state before the push.

## Troubleshooting

- "override file left from an earlier run": an interrupted tfcensus run left
  `_tfcensus_override.tf` in a unit. Make sure no other run is active, delete the file, and start
  again. tfcensus never deletes it for you. While it is there, terraform in that unit sees a local
  backend instead of s3 at its next init and asks whether to migrate the state. Answer no, then
  delete the file.
- "the unit's remote_state must use generate": add a `generate` block to the `remote_state` in
  your root configuration.
- "state changed since it was read": someone applied a unit during the run. Plan and apply again.
- "state is locked": terraform or another tfcensus run holds the lock. Wait for it, or remove a
  stale lock with `terraform force-unlock` in the unit.
- Verification passes but the destination still plans changes after the merge: the plan ran on a
  branch whose configuration differs from what was merged. Plan the units again and fix the code.

## Translate a tfmigrate file

tfcensus doesn't read tfmigrate files. A `multi_state` migration becomes a block with one
destination:

```hcl
# tfmigrate
migration "multi_state" "mv_sg" {
  from_dir = "live/network"
  to_dir   = "live/network-sg"
  actions  = ["mv aws_security_group.app aws_security_group.app"]
}

# tfcensus
migration "units" "mv_sg" {
  from = "live/network"
  to   = { "live/network-sg" = ["aws_security_group.app"] }
}
```

A `multi_state` mv that also renames the address becomes the move block plus an in-place block on
the destination with the `mv`. A `state` migration keeps its `actions` and becomes
`from = "<dir>"`. tfmigrate's history file and
`.tfmigrate.hcl` don't carry over; run `move mark-applied` for migrations tfmigrate already applied.
