---
description: How tfcensus finds Terragrunt units and plain Terraform root modules, how it resolves each one's state backend and target account, and when it runs terragrunt render.
---

# Units, repos and workspaces

Every command that reads state starts by finding units. A unit is one Terragrunt unit or one plain
Terraform/OpenTofu root module, with one state backend. This page says how `tfcensus units` finds
them and how it works out where each one keeps its state. `inventory`, `scan`, `audit backend`,
`whois` and `drift` find units the same way.

## Repos

Each path you pass is a repo, and the `REPO` column shows it as you typed it. Without paths,
tfcensus uses `discovery.roots` from the config, or else the working directory.

A path is a Terragrunt repo when a `terragrunt.hcl`, `root.hcl` or `terragrunt.stack.hcl` exists
anywhere below it. tfcensus skips `.git`, `.terraform`, `.terragrunt-cache` and `node_modules`
while it looks. Any other path is a plain Terraform repo.

## Terragrunt units

In a Terragrunt repo, tfcensus runs `terragrunt find --json --dag --dependencies --exclude` and
takes the units and stacks it returns. `--filter` passes a filter expression to `terragrunt find`
unchanged, such as `--filter './live/prod/**'`. In a plain Terraform repo `--filter` does nothing,
and tfcensus logs a warning.

A unit that an `exclude` block turns off for every command is still listed, with the warning
`excluded by terragrunt for: all`.

## Plain Terraform root modules

In a plain repo, a root module is a directory whose `.tf` or `.tf.json` files have a `backend` or
`cloud` block. A directory without one is a child module and isn't listed. tfcensus parses the
files and runs nothing, so the resolution is `static`. When a module has more than one backend
block, tfcensus uses the first and adds a warning. A backend attribute that isn't a literal value
leaves the unit `unresolved`, because nothing evaluates variables in a backend block.

## Fast path and render

For a Terragrunt unit, tfcensus evaluates the `remote_state` block itself. This is the fast path,
and it starts no process. It knows the Terraform functions and the Terragrunt functions that locate
state: `find_in_parent_folders`, `read_terragrunt_config`, `path_relative_to_include`,
`get_terragrunt_dir`, `get_repo_root`, `get_env` and others. `get_env` reads tfcensus's own
environment here.

`get_aws_account_id` returns the account of the default AWS credentials (`AWS_PROFILE`,
environment credentials or an instance role). tfcensus looks it up once per run with
`sts:GetCallerIdentity`, and only when a unit's state location needs it.

Some functions need a command or an AWS call that the fast path doesn't make: `run_cmd`,
`sops_decrypt_file`, `get_aws_caller_identity_arn`, `get_aws_account_alias` and a few more. A unit
that calls one of them in its state location goes to `terragrunt render --json`. So does a unit that
calls `get_aws_account_id` while it sets `iam_role`, or while `TG_IAM_ASSUME_ROLE` is set, because
terragrunt would assume that role first.

`--render` (config `discovery.render`) picks the method:

| Value | What happens |
|---|---|
| `auto` | the fast path first, then `terragrunt render` for the units it can't resolve (default) |
| `always` | `terragrunt render` for every unit |
| `never` | the fast path only; units it can't resolve stay `unresolved` |

`terragrunt render` runs with `--no-dependency-outputs`, so it doesn't read the state of other
units, and with a five-minute timeout per unit. The
[terragrunt concept page](/concepts/terragrunt) says which environment it gets.

The `RESOLUTION` column says which method worked: `fast-path`, `render`, `static` or `unresolved`.
An unresolved unit has its reason under the table. Here the `dns` unit calls `run_cmd` and render
resolved it, and the `secrets` unit failed both ways:

```sh
tfcensus units tg-live
```

<<< @/../testdata/golden/units/tg-live.txt

## Stacks

A `terragrunt.stack.hcl` file shows as a `terragrunt-stack` row. Until its units are generated,
`terragrunt find` can't list them, and the row carries the warning
`not generated: run with --generate-stacks to list its units`.

`--generate-stacks` (config `discovery.generate_stacks`) runs `terragrunt stack generate` in each
Terragrunt repo first. That command writes the generated units into the repo's working tree, in the
`.terragrunt-stack` directory next to each stack file. tfcensus changes nothing else in a repo.

## Target account and region

Each unit also gets the AWS account and region its resources live in. tfcensus takes the first
source that has a value:

1. The default `aws` provider block: `allowed_account_ids` (one ID), the account in
   `assume_role`, and `region`. Confidence `high`, or `medium` when the value comes from a variable
   default, which a tfvars file can override.
2. A Terragrunt unit's inputs: `aws_account_id` or `account_id`, and `aws_region` or `region`.
   Confidence `medium`.
3. The backend: a 12-digit number in the bucket name, the account of the backend's `role_arn`, and
   the backend region. Confidence `low`.

The `ACCOUNT` column adds `(medium)` or `(low)` when the account came from a weaker source, and
`--format json` shows the source of each value in `account_from` and `region_from`. When a
resource's identity and ARN don't name its account and region, `scan` and `whois` use the unit's
target, so a `low` target is worth a look. `allowed_account_ids` in the provider block raises it to
`high`.

## Workspaces

`units` lists one row per unit. `inventory` reads every workspace of a unit: it lists the
backend's `workspace_key_prefix` (default `env:`) with `s3:ListBucket`, and shows each workspace as
its own row. [How state is read](/concepts/state) has the details.

## Related

- [`tfcensus units`](/cli/units) lists every flag.
- [How state is read](/concepts/state) covers what happens after discovery.
- [Terragrunt and terraform](/concepts/terragrunt) covers the commands tfcensus runs.
