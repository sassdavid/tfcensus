# tfcensus ADR-0001: Shell out to terragrunt/terraform/tofu, never import their internals

- Status: accepted (2026-11-09). M1 spike result added 2026-11-10.

## Context
- Terragrunt v1.x keeps almost everything under `internal/`. `pkg/` exports only config, log, options and venv, with no stability promise. terramate is stuck importing v0.82.0.
- Terraform core is BSL 1.1. OpenTofu's code is MPL, but its packages are internal.
- driftctl died partly because it was coupled to Terraform 0.14 internals and to provider binaries.

## Decision
- Drive the binaries through `internal/execx`:
  - `terragrunt find --json`, `render --json`, `stack generate`, `info print`, `run --all … --json-out-dir --report-file --log-format json`, `run -- state pull|push`
  - `terraform|tofu show -json`, `providers schema -json`
- Use the MPL libraries for data (terraform-json, hcl/v2, terraform-exec). terraform-config-inspect was
  dropped at M1: it doesn't expose backend or cloud blocks and pulls in HCL v1, so plain root modules
  are read with hcl/v2.
- Minimum versions and feature gates follow [engineering/version-policy.md](../../engineering/version-policy.md):
  - terragrunt ≥ 1.0.0 (the stability guarantee; every needed feature is GA by then);
  - terraform ≥ 1.5 / tofu ≥ 1.6 for exec features, with newer features gated by detected version;
  - state files are readable from any v4 format.
- `find` JSON and the run-report schema are covered by Terragrunt's 1.x guarantees. `render` and `info print` are **not**, so parse those leniently.
- Always tell Terragrunt the binary, because it defaults to `tofu`: set `TG_TF_PATH` on every call and
  also pass `--tf-path` to the subcommands that accept it (`run`, `render`). `find` and the global flags
  reject `--tf-path` (checked on 1.2.1).
- Contract tests run against pinned real binaries in the nightly job, so CLI or JSON shape changes fail loudly.

## M1 spike result

Checked on 2026-11-10 with terragrunt v1.2.1 and terraform v1.17.1. Every run had AWS credentials
blanked (`AWS_CONFIG_FILE=/dev/null`, no keys, IMDS off), so nothing could reach AWS.

Does `terragrunt render --json` include the merged `remote_state`? **Yes.** The JSON has
`remote_state.backend` and `remote_state.config` with bucket, key, region, `use_lockfile`,
`dynamodb_table` and the rest, after includes and functions are evaluated
(`pkg/config/config_as_cty.go` serializes it). Other findings that shaped the design:

- `render --all --json` prints one object per unit, concatenated, with no unit path. When a unit
  fails, the objects after it can't be matched to units. tfcensus renders one unit per call:
  `render --working-dir <unit> --json --no-dependency-outputs --tf-path <bin>`, in parallel, with a
  5-minute timeout per unit.
- By default render reads each dependency's outputs from its S3 state (it tried to build an S3
  client). `--no-dependency-outputs` turns that off. tfcensus never needs those outputs.
- render evaluates every local. A unit with `get_aws_account_id()` in an unused local fails without
  credentials, even though its `remote_state` doesn't need it.
- `find --dag` sorts but doesn't list dependencies. `--dependencies` adds them and `--exclude` adds
  the exclude block. After `stack generate`, find lists the stack and its units under
  `.terragrunt-stack/`.
- Terragrunt makes dependency paths relative to the symlink-resolved directory. A symlinked repo path
  (such as macOS `/var` → `/private/var`) gave paths like `../../../private/...`. tfcensus resolves
  symlinks before it runs terragrunt.
- tfcensus doesn't pass `--non-interactive`, because it means "assume yes" to every prompt. Child
  stdin is `/dev/null`, so no prompt can block a run.

Chosen approach: the fast path is the default (`discovery.render: auto`). tfcensus evaluates
`remote_state`, `generate` and `inputs` with hcl/v2, cty's standard functions and the path functions
of Terragrunt (`find_in_parent_folders`, `path_relative_to_include`, `read_terragrunt_config`,
`get_env`, `get_terragrunt_dir`, `get_repo_root` and related ones). It evaluates locals one by one,
so a local that fails only breaks the expressions that use it. Functions that need a command or AWS
(`run_cmd`, `sops_decrypt_file`, `get_aws_account_id`) fail on the fast path, and those units go to
render. `always` and `never` force one method. Each unit reports the method used, and unresolved
units carry the reason from both methods.

Validation, read-only, on 18 private Terragrunt repos from the author's work (1,601 units): the fast
path resolved 1,600 units and render 1,580. Where both resolved a unit, backend, account and region
were identical. The unit neither resolved has an include file that doesn't exist. Render failed on
the other 20 because of `dependencies` paths that don't exist, which the fast path doesn't read.

Performance: the fast path took 0.7 s for 197 units and 0.6 s for 426 units, `terragrunt find`
included. Render for the same 197 units took 2.7 s on 14 CPUs. On a generated 300-unit tree the fast
path takes about 40 ms (`BenchmarkDiscover300Units`, `TestSpec_discovery_5`).

The fixture recordings in `testdata/terragrunt/` come from `scripts/record-terragrunt.sh`. Run it
again after a terragrunt upgrade and review the diff.
