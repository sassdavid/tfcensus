# Spec: unit discovery and backend resolution (F1)

## Inputs (tried in order per path)
1. **Terragrunt repo** (a `root.hcl`, `terragrunt.hcl` or `terragrunt.stack.hcl` exists anywhere under the path): `terragrunt find --json --dag --dependencies --exclude` gives units (hidden directories are included by default since 1.0; never pass the deprecated `--hidden`) (`type: unit|stack`, `path`, `dependencies`).
   - Stacks: run `terragrunt stack generate` first if `--generate-stacks` is set (it writes `.terragrunt-stack/`). Otherwise list stacks as "not generated" with a hint.
   - Selection: `--filter` is passed straight through to Terragrunt (v1.2 filter syntax: paths, `name=`, `type=`, `reading=`, graph `...`, git `[main...HEAD]`).
2. **Plain Terraform/OpenTofu** root modules: directories with `*.tf` containing a `backend` block or a `terraform { cloud {} }` block, read with hcl/v2 (terraform-config-inspect doesn't expose backend blocks). No exec.
3. **Bucket scan** (`--scan-buckets <bucket…>` or `audit backend --discover`; needs the AWS clients from accounts, so it comes after M2): list `*.tfstate` keys, including `env:/<workspace>/` prefixes, and skip `*.tflock` and `-md5` items. Gives "state-only units" with no code location, which is how orphans are found.

## Backend resolution (per Terragrunt unit)
- **Spike (M1), answered yes:** `terragrunt render --json` (v1.2.1) includes the merged `remote_state` block. Details and measurements: [adr/0001](../adr/0001-shell-out-to-terragrunt.md#m1-spike-result).
  - Render: `render --working-dir <unit> --json --no-dependency-outputs` per unit (`--all` output can't be matched to units). It evaluates includes and functions, but may run `run_cmd`/sops, so it runs with a timeout, in parallel, with stdin closed.
  - Fast path, the default (`discovery.render: auto`): evaluate the common pattern ourselves: `remote_state { backend="s3" config={ bucket=…, key="${path_relative_to_include()}/terraform.tfstate", region=…, role_arn/assume_role, use_lockfile, dynamodb_table } }` with hcl/v2 + a small function set (`path_relative_to_include`, `get_aws_account_id` from the resolved profile, `local.*`, `read_terragrunt_config` for `account.hcl`/`region.hcl`/`env.hcl` patterns).
  - `get_aws_account_id` returns the account of the default AWS credentials (`AWS_PROFILE`, environment, instance role), from one `sts:GetCallerIdentity` per run. Locals are evaluated only when used, so a local that calls it costs nothing unless state needs it. A unit that also sets `iam_role` (or `TG_IAM_ASSUME_ROLE`) goes to render, because terragrunt would answer with the role's account.
  - The resolver reports per unit which method it used. Unresolvable units are listed with the reason, never silently dropped.
- **Account and region of managed resources:** from the aws provider block (`allowed_account_ids`, `assume_role.role_arn`, `region`), which Terragrunt repos usually write in a `generate` block, then from inputs (`aws_account_id`, `account_id`, `aws_region`, `region`). Fallback: the account that owns the state bucket, taken from a 12-digit ID in the bucket name or the backend role ARN, and the backend region. `confidence` is high (provider), medium (inputs, or a provider value through a variable default) or low (guessed or unknown).

## Output (`tfcensus units`)
Columns: repo, unit path, kind, backend (bucket/key/region), lock (lockfile|dynamodb|both|none), target account, region, resolution method, warnings.

## Acceptance criteria
- [x] Fixture repo `testdata/repos/tg-live/` (root.hcl + account.hcl/region.hcl/env.hcl hierarchy, 12 units, 1 stack, 1 excluded unit) → golden `units --format json`, using a fake `terragrunt` binary that returns recorded `find`/`render` output.
- [x] Plain-TF fixture (3 roots, one with `cloud {}` → reported as unsupported backend, not an error).
- [x] Spike result recorded in `adr/0001` (render includes remote_state: yes/no) with Terragrunt version.
- [x] `--filter './live/prod/**'` reaches terragrunt unchanged (asserted via the fake binary's argv log).
- [x] 300 units resolve in under 10 s (fast path, no exec), benchmarked on a generated tree.
