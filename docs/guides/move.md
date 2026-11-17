---
description: Move resources between Terragrunt units with tfcensus move. Draft the file from plans, verify it, apply it, roll back from S3 object versions, or use the native strategy.
---

# Move resources between units

This guide moves resources from one unit's state to other units' states, in the same account or
in another one, with `tfcensus move`. It also covers renames inside one state, rollback and the
native strategy for teams that apply only through pull requests. Every attribute of the migration
file is on the [move file reference](/reference/move-file).

For a refactor inside a single plain Terraform directory, tfmigrate or `moved` blocks are the
better tools. Use `tfcensus move` when resources cross unit boundaries.

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
- `move suggest` drafts the file for you from plans, after you've moved the code.

`suggest`, `plan` and `status` only read. `apply`, `rollback` and `mark-applied` are the only
tfcensus commands that write to a backend, and none of them is an MCP tool. The
[safety model](/concepts/safety) lists the guards around each write.

## Before you start

Check each unit the migration touches:

1. It has an `s3` backend that `tfcensus units` resolves, with bucket, key and region shown. For
   a workspace other than default, the workspace already exists (`terraform workspace list`).
2. Its terragrunt `remote_state` uses a `generate` block. Verification puts a local backend in
   front of the unit through `_tfcensus_override.tf`, and that only works with a generated backend.
3. Its state bucket has versioning on. `tfcensus audit backend` reports a bucket without it as
   S3-VER. Apply refuses such a unit (MV-VERSIONING), because a push couldn't be rolled back.
4. Nobody else applies the unit while you work. Pause the CI or Atlantis jobs for these units.
   tfcensus checks that no state changed between its read and its push and stops if one did, but
   it can't stop a run that starts after the push.

Your credentials need the permissions in the
[move permissions table](/reference/iam#move-permissions) for every involved backend. tfcensus
reads each unit with that unit's own backend credentials: the backend's `profile`, or its
`role_arn` assumed from the profile that `accounts.map` names for the bucket's account, or the
default credentials. Terragrunt runs in each unit with the unit's own configuration. No single
call holds two accounts' credentials.

tfcensus passes `terraform` to terragrunt by default, because terragrunt itself defaults to
`tofu`. For OpenTofu, pass `--tf-path tofu` or set `binaries.terraform` in `.tfcensus.yaml`.

## Draft the file with move suggest

Move the resource blocks in the code first: delete them from the source unit and add them to the
destinations, or rename them. Every plan then shows a delete in the source and a create in a
destination. `move suggest` pairs them and writes the migration file:

```sh
tfcensus move suggest --from live/prod/network \
  --to live/prod/network-sg --to live/prod/network-endpoints \
  --draft migrations/2026-11-split-network.hcl
```

It plans each unit in turn through terragrunt, against its backend and without the state lock.
To reuse plans you already have, pass `--plan-dir` with the directory that
`terragrunt run --all --json-out-dir` wrote. suggest then reads `<dir>/<unit>/tfplan.json` and
runs nothing.

A delete and a create are one move when their type is the same, every attribute known before
apply is equal, and neither matches any other resource. Null equals an empty string, list or map.
Name a unit in both `--from` and `--to` to find renames inside it. A replaced resource or a data
source never counts. The summary on stderr lists every move, then every delete and create it left
out, with the reason:

```text
not moved:
  delete live/prod/network aws_vpc_endpoint.s3: closest is live/prod/network-endpoints module.endpoints.aws_vpc_endpoint.this["s3"], which differs in policy
  delete live/prod/network aws_vpc_endpoint_route_table_association.s3: 2 matches (...)
```

When a provider rewrites a value, so that your code never matches the state, let that attribute
differ with `--ignore`. The syntax is tfautomv's, so its rules work here too:

| Rule | Counts as equal |
|---|---|
| `everything:TYPE:ATTR` | any two values |
| `whitespace:TYPE:ATTR` | values that differ only in whitespace |
| `prefix:TYPE:ATTR:PREFIX` | values that are equal once PREFIX is removed |
| `json:TYPE:ATTR` | JSON strings with the same value, such as a policy in another key order |

TYPE may be `*`. ATTR is a flat key (`tags.Name`, `ingress.0.cidr_blocks.#`). A rule covers the
keys below it, so `everything:aws_instance:tags` ignores every tag, and a `*` segment matches any
one segment. Use `--ignore` for provider quirks only. A difference you made on purpose belongs in a
separate change after the move.

The draft has one block per source unit, then one block of `mv` actions per unit whose addresses
change. Read it, rename the blocks if you like, then verify it with `move plan` as below.

## Write the migration file by hand

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
same in the destination. The reference lists the
[address forms a `to` entry takes](/reference/move-file#addresses-in-to), the
[`mv`, `xmv` and `rm` actions](/reference/move-file#actions) and
[variables](/reference/move-file#variables) for a file that serves several environments, such as
`from = "live/${var.env}/network"` with `--var env=staging`.

For a workspace other than default, append `:<workspace>` to the unit path, such as
`live/network:blue`. tfcensus then reads and writes that workspace's state where terraform keeps
it: `<workspace_key_prefix>/<workspace>/<key>`, with `env:` when the backend sets no prefix. The
lock, the DynamoDB digest and the history object follow the same key, and terragrunt runs with
`TF_WORKSPACE`. Two workspaces of one unit are two states, so a block can move resources from
`live/network:blue` to `live/network:green`. The workspace must exist: tfcensus stops before
planning (exit 2) when a workspace has no state, so run `terraform workspace new <name>` in the
unit first.

## Run the move

1. Change the code first, in a branch. Move each resource's configuration into its destination
   unit and remove it from the source, then add the migration file. Don't merge yet.
2. Plan on that branch. Each unit is planned with the branch's configuration:

   ```sh
   tfcensus move plan migrations/2026-11-split-network.hcl
   ```

   The output lists each unit's role, how many addresses move in or out, and the serial change. A
   new destination state shows `(new state)`. Each block ends `verified` or `rejected`, with the
   findings that rejected it. The exit code is 3 when a block is rejected.

3. Read every finding before you go on. The [findings table](/reference/move-file#findings)
   describes each rule. The usual fixes:

   | Rule | Fix |
   |---|---|
   | MV-VERIFY | Fix the configuration or the provider, then plan again. A create in the destination often means its provider points at another account, so refresh found nothing. |
   | MV-PLAN | Run `terragrunt plan` in the unit to see why. Terraform's output stays out of the report, because it can hold attribute values. |
   | MV-ACCOUNT | Move the resource to a unit in its own account. |
   | MV-VERSIONING | Turn versioning on for the state bucket. |

4. Apply. tfcensus plans again, then asks you to type the block's last destination unit path (the
   `from` path for an in-place block):

   ```sh
   tfcensus move apply migrations/2026-11-split-network.hcl
   ```

   In CI, pass `--yes`. Without a terminal and without `--yes`, apply exits 2 and changes nothing.
   Each push is logged at info level with the unit and its `s3://` location.

5. Check the result. Plan each unit through your normal flow; every plan should show no changes.
   `tfcensus move status <file>` shows the block as applied in every unit.
6. Merge the branch and resume the paused jobs.

Applying the same file again skips blocks that are already applied. `--block` runs only the named
blocks.

## Roll back

```sh
tfcensus move rollback migrations/2026-11-split-network.hcl
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
and apply refuses to run it again. There are two ways out:

- Run `move rollback` for the block. It restores the units that have a record.
- Finish the remaining pushes by hand, then run `move mark-applied` so the history matches.

A stale read stops apply before the first push: every unit's current S3 version is compared with
the one tfcensus read, so a run that changed a state in between doesn't leave a half-done block.
The error messages a move can stop with, and what to do about each, are on the
[troubleshooting page](/troubleshooting).

## Native strategy for pull-request flows

Teams that change state only through reviewed applies (Atlantis, Digger, CI) can set
`strategy = "native"` on a block. tfcensus then writes files instead of pushing states:

- Each destination gets `moves_tfcensus_<block>.tf` with one `import` block per moved instance, by
  identity when the state has one (terraform or OpenTofu 1.12 and later), else by `id`.
- The source gets the same file name with `removed` blocks and `destroy = false`.

`move plan` lists the files. `move apply` writes them (`--force` overwrites earlier ones) and plans
each unit: destinations may only import with no changes, and the source may only forget. Commit the
files with the configuration change, let your normal flow apply them, delete the files afterwards,
and run `tfcensus move mark-applied <file>` to record the migration.

Check the `id` imports: for some resource types the import ID isn't the `id` attribute, and the
plan then fails the import. A move of only some instances of a resource, or of resources inside a
module instance with a key, can't be written as `removed` blocks and needs the default strategy.
The native strategy works on the default workspace only, because its files are part of the
configuration that every workspace applies.

## Migrations done by hand

`tfcensus move mark-applied <file>` writes a history record in every unit of each block without
touching any state. tfcensus can't roll back such records, so restore those states by hand if you
need to. The [history object](/reference/move-file#history) shows what a record holds.

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
`from = "<dir>"`. tfmigrate's history file and `.tfmigrate.hcl` don't carry over. Run
`move mark-applied` for migrations tfmigrate already applied.
