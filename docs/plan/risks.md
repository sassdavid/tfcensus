# tfcensus: risks

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | **Type-mapping maintenance**, the reason driftctl stalled | high | high | Data-driven typemap generated from provider schemas and awscc ([ADR-0004](adr/0004-identity-arn-id-matching.md)); identity coverage grows upstream; unmapped → `undetermined`, never a false "managed" |
| R2 | False positives erode trust | high | high | Built-in ownership packs, CEL rules with `--explain`, a measured FP rate under 5% before v0.1, categories instead of one "unmanaged" bucket |
| R3 | AWS API throttling or slowness at Org scale | medium | medium | Capability-probed layers, the Config aggregator fast path, adaptive retry, per-layer caches, Cloud Control only for gap types |
| R4 | `terraform query` / list resources mature and cover part of the use case | medium | medium | Complement rather than compete: codify emits `.tfquery.hcl`. tfcensus does multi-state, multi-account coverage and backend audit, which `query` (per-config, single provider) doesn't |
| R5 | Terragrunt CLI or JSON churn (v1.x moves fast) | high | medium | Minimum version check, contract tests against pinned binaries nightly, the fast-path backend evaluator as a fallback |
| R6 | OpenTofu encrypted state is unreadable | medium | low | Detect and report; optional `tofu show -json` path |
| R7 | State contains secrets | certain | high | Never print attributes; identifiers only; a grep test in CI; docs on IAM scoping |
| R8 | `move` corrupts state | low | critical | Plan verification, versioning required, VersionId rollback, typed confirmation, an e2e against moto (two emulated accounts) with rollback, the native (PR-based) strategy as a safer alternative |
| R9 | Scope creep into Atlantis, Terramate or Spacelift territory | medium | high | Explicit non-goals in [overview.md](overview.md); every feature must reuse F1–F4 |
| R10 | Maintainer bandwidth | high | medium | relgap ships first. tfcensus v0.1 is scoped to F1–F3 + scan + audit. Contributors are attracted through the typemap/rules data |
| R11 | Using the author's employer environment as a test bed | medium | medium | Live validation is read-only, uses named profiles explicitly approved per session, and records only aggregated results in `docs/validation/` (no account IDs in the public repo) |
