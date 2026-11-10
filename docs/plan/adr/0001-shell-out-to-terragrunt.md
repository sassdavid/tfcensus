# tfcensus ADR-0001: Shell out to terragrunt/terraform/tofu, never import their internals

- Status: accepted (2026-11-09). The M1 spike result is to be appended.

## Context
- Terragrunt v1.x keeps almost everything under `internal/`. `pkg/` exports only config, log, options and venv, with no stability promise. terramate is stuck importing v0.82.0.
- Terraform core is BSL 1.1. OpenTofu's code is MPL, but its packages are internal.
- driftctl died partly because it was coupled to Terraform 0.14 internals and to provider binaries.

## Decision
- Drive the binaries through `internal/execx`:
  - `terragrunt find --json`, `render --json`, `stack generate`, `info print`, `run --all … --json-out-dir --report-file --log-format json`, `run -- state pull|push`
  - `terraform|tofu show -json`, `providers schema -json`
- Use the MPL libraries for data (terraform-json, hcl/v2, terraform-exec, terraform-config-inspect).
- Minimum versions and feature gates follow [engineering/version-policy.md](../../engineering/version-policy.md):
  - terragrunt ≥ 1.0.0 (the stability guarantee; every needed feature is GA by then);
  - terraform ≥ 1.5 / tofu ≥ 1.6 for exec features, with newer features gated by detected version;
  - state files are readable from any v4 format.
- `find` JSON and the run-report schema are covered by Terragrunt's 1.x guarantees. `render` and `info print` are **not**, so parse those leniently.
- Always pass `--tf-path`, because Terragrunt defaults to `tofu`.
- Contract tests run against pinned real binaries in the nightly job, so CLI or JSON shape changes fail loudly.

## M1 spike (to fill in)
- Does `terragrunt render --json --all` include merged `remote_state`? → _TBD_
- The chosen backend resolution path (render vs our fast-path evaluator) and its performance on 300 units → _TBD_
