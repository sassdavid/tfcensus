---
description: Which terragrunt, terraform and tofu commands tfcensus runs, which environment variables reach them, how it reads TFCENSUS_* variables, and how logs and Ctrl-C work.
---

# Terragrunt and terraform

tfcensus reads state and plan files itself, but it runs terragrunt, terraform and OpenTofu for some
steps. This page lists what it runs, which binary, which environment the child process gets, and
how to see it in the logs.

## What runs

| Step | Command | Run by |
|---|---|---|
| List units | `terragrunt find --json --dag --dependencies --exclude` | every command that finds units in a Terragrunt repo |
| Resolve a unit | `terragrunt render --json --no-dependency-outputs` | the same, for units the fast path can't resolve ([units](/concepts/units)) |
| Generate stacks | `terragrunt stack generate` | with `--generate-stacks` |
| Check versions | `terragrunt --version`, `terraform version` | `doctor` |
| Plan for drift | `terragrunt run --all ... -- plan -refresh-only -lock=false -detailed-exitcode` | `drift`, unless `--from-json-out-dir` is set |
| Move resources | `terragrunt run -- init`, `plan`, `show` and `state push` in each unit | `move plan`, `move apply` and `move suggest` |
| Detect the engine | `terraform version -json` in the target directory | `codify` |
| Read encrypted state | `tofu init` and `tofu show -json` in the unit | `--decrypt-with-tofu` ([encrypted state](/guides/encrypted-state)) |

In a plain Terraform repo, discovery runs nothing: tfcensus parses the `.tf` files itself. `scan`,
`audit backend`, `inventory` and `whois` run nothing beyond discovery. `drift` skips plain
Terraform root modules.

The [`drift`](/cli/drift) and [`move`](/cli/move/) pages give the full command lines. `move` and
the decrypt step give each run its own `TF_DATA_DIR` and terragrunt download directory in a
temporary folder, and put the unit's `.terraform.lock.hcl` back afterwards, so the unit's
`.terraform` and `.terragrunt-cache` stay as they were.

## Which binary

terragrunt runs OpenTofu unless it's told otherwise. tfcensus tells it every time: it sets
`TG_TF_PATH` on every terragrunt call, and adds `--tf-path` to `run` and `render` (`find` rejects
the flag). The binary is `--tf-path` (config `binaries.terraform`, default `terraform`). For an
OpenTofu fleet, set it to `tofu`.

| Flag | Config key | Default | Used for |
|---|---|---|---|
| `--tg-path` | `binaries.terragrunt` | `terragrunt` | every terragrunt call |
| `--tf-path` | `binaries.terraform` | `terraform` | passed to terragrunt, and run directly by `doctor` and `codify` |
| `--tofu-path` | `binaries.tofu` | `tofu` | `--decrypt-with-tofu` |

A name without a slash is looked up on the `PATH` that the child process gets. The
[supported versions](/supported-versions) page lists the oldest version of each tool.

## The environment of a child process

A child process doesn't get tfcensus's whole environment. It gets the variables on an allowlist,
plus the ones tfcensus sets for that call, such as `TG_TF_PATH`. The allowlist has the basics
(`PATH`, `HOME`, `TMPDIR`, the locale), the proxy and certificate variables, and every variable that
starts with `AWS_`, `TF_`, `TG_`, `TERRAGRUNT_`, `TOFU_`, `OPENTOFU_`, `MISE_`, `GIT_` or `XDG_`.
The [environment reference](/reference/environment#child-processes) lists every name.

So `TF_VAR_*`, `TG_*` settings, `AWS_PROFILE` and `AWS_ENDPOINT_URL` reach terragrunt and
terraform. A variable outside the list doesn't. A Terragrunt configuration that calls
`get_env("DEPLOY_ENV")` gets an empty value in `terragrunt render` and in `drift`, even when your
shell sets it. Rename the variable to one with an allowed prefix, such as `TG_DEPLOY_ENV`, or give
`get_env` a default.

The fast path is different: it evaluates `get_env` in tfcensus's own process, with the full
environment. A unit can then resolve on the fast path with a value that `terragrunt render` wouldn't
see.

terraform and terragrunt also use their own AWS credentials: the variables and profiles in this
environment and the unit's configuration. tfcensus's choice of credentials for reading state
([how state is read](/concepts/state)) doesn't reach them. When `drift` or `move` fails on access
while `inventory` works, check the credentials terragrunt itself would use.

## Variables tfcensus reads

Every config key has a variable: `TFCENSUS_` followed by the key in upper case, with dots and
dashes as underscores. `cache.ttl` is `TFCENSUS_CACHE_TTL`, and `scan.packs.aws-defaults` is
`TFCENSUS_SCAN_PACKS_AWS_DEFAULTS`. A double underscore marks a level explicitly, as in
`TFCENSUS_DISCOVERY__GENERATE_STACKS=true`. Three rules apply:

- Only scalars and string lists can be set this way. A list is comma-separated, as in
  `TFCENSUS_REGIONS=eu-west-1,eu-central-1`.
- Maps and lists of objects, such as `accounts.map`, `rules` and `drift.noise`, need a config file.
- An unknown `TFCENSUS_*` variable is ignored with a warning, so a typo doesn't stop the run.
  `tfcensus config show --sources` shows which layer each value came from.

The [configuration reference](/reference/config) names the variable of every key. The
[environment reference](/reference/environment) lists the other variables tfcensus reads, among
them `CI`: any value turns prompts off, as running without a terminal does, so `move apply`,
`move rollback` and `move mark-applied` then need `--yes`.

## Timeouts and Ctrl-C

One `terragrunt render` may run 5 minutes and a version check 30 seconds. A `drift` run and the
`move` steps have no time limit, and other child processes have 30 minutes.

On Ctrl-C, tfcensus sends each running child one interrupt, so terraform can release a state lock,
and kills it 10 seconds later if it is still running. Children run in their own process group, so
the terminal's Ctrl-C doesn't reach them twice. tfcensus then prints `tfcensus: interrupted` and
exits 130.

## Logs

Logs go to stderr and reports to stdout, so `--format json` output stays clean in a pipe. The
default level is warn. `-v` adds info, `-vv` debug, `-vvv` trace, and `-q` keeps errors only.

At debug level, each child process logs its binary, arguments, directory and the names of the
variables tfcensus set for it, and then its exit code and duration. Values of flags and variables
whose names look secret, such as `--token` or `-var db_password=...`, show as `***`.

`--log-format json` writes one JSON object per line, for a log collector. `--log-file <path>`
writes the logs to a file instead of stderr. tfcensus creates the file with mode 0600 or appends to
it.

```sh
tfcensus drift ~/src/infra/acme-live -vv --log-format json --log-file drift.log
```
