# Spec: unit discovery and backend resolution (F1)

## Inputs (tried in order per path)
1. **Terragrunt repo** (a `root.hcl`/`terragrunt.hcl` exists): `terragrunt find --json --dag` gives units (hidden directories are included by default since 1.0; never pass the deprecated `--hidden`) (`type: unit|stack`, `path`, `dependencies`).
   - Stacks: run `terragrunt stack generate` first if `--generate-stacks` is set (it writes `.terragrunt-stack/`). Otherwise list stacks as "not generated" with a hint.
   - Selection: `--filter` is passed straight through to Terragrunt (v1.2 filter syntax: paths, `name=`, `type=`, `reading=`, graph `...`, git `[main...HEAD]`).
2. **Plain Terraform/OpenTofu** root modules: directories with `*.tf` containing a `backend` block or a `terraform { cloud {} }` block, found via terraform-config-inspect. No exec.
3. **Bucket scan** (`--scan-buckets <bucket…>` or `audit backend --discover`): list `*.tfstate` keys, including `env:/<workspace>/` prefixes, and skip `*.tflock` and `-md5` items. Gives "state-only units" with no code location, which is how orphans are found.

## Backend resolution (per Terragrunt unit)
- **Spike first (M1):** does `terragrunt render --json` (v1.2.1) include the merged `remote_state` block? The docs only show locals and inputs.
  - If yes: use `render --json --all` once per repo. It evaluates includes and functions, but may run `run_cmd`/sops, so run it with a timeout, in parallel, honouring `--no-input`.
  - If no, or as a fast path: evaluate the common pattern ourselves: `remote_state { backend="s3" config={ bucket=…, key="${path_relative_to_include()}/terraform.tfstate", region=…, role_arn/assume_role, use_lockfile, dynamodb_table } }` with hcl/v2 + a small function set (`path_relative_to_include`, `get_aws_account_id` from the resolved profile, `local.*`, `read_terragrunt_config` for `account.hcl`/`region.hcl`/`env.hcl` patterns).
  - The resolver reports per unit which method it used. Unresolvable units are listed with the reason, never silently dropped.
- **Account and region of managed resources:** from the provider block (`assume_role.role_arn`, `allowed_account_ids`) or from `account.hcl` inputs. Fallback: the account that owns the state bucket. Marked `confidence: low` when guessed.

## Output (`tfcensus units`)
Columns: repo, unit path, kind, backend (bucket/key/region), lock (lockfile|dynamodb|both|none), target account, region, resolution method, warnings.

## Acceptance criteria
- [ ] Fixture repo `testdata/repos/tg-live/` (root.hcl + account.hcl/region.hcl/env.hcl hierarchy, 12 units, 1 stack, 1 excluded unit) → golden `units --format json`, using a fake `terragrunt` binary that returns recorded `find`/`render` output.
- [ ] Plain-TF fixture (3 roots, one with `cloud {}` → reported as unsupported backend, not an error).
- [ ] Spike result recorded in `adr/0001` (render includes remote_state: yes/no) with Terragrunt version.
- [ ] `--filter './live/prod/**'` reaches terragrunt unchanged (asserted via the fake binary's argv log).
- [ ] 300 units resolve in under 10 s (fast path, no exec), benchmarked on a generated tree.
