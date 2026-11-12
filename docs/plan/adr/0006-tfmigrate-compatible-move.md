# tfcensus ADR-0006: Move migrations are tfmigrate-compatible in spirit, unit-aware and plan-verified

- Status: accepted (2026-11-09). State pulls, rollback and confirmation are refined by [ADR-0011](0011-move-state-io-and-rollback.md).

## Context
tfmigrate (MIT, v0.4.6, active) has proven the migration-file model and plan-verified dry runs. Its
limits for Terragrunt fleets:
- global history (#200), no mark-applied (#220), output diffs fail verification (#221), one block per file (#217);
- dynamic Terragrunt backends break (#167); `remote_state` must use `generate`;
- it relies on the deprecated `-state` flag (#202).

`terragrunt backend migrate` moves whole states only (#6350 open).

## Decision
- Our own `migration "units"` blocks reuse tfmigrate's action vocabulary (`mv`, `xmv`, `rm`, `import`). The verbs carry over, so tfmigrate users recognize them.
- Units are addressed by Terragrunt path. State I/O goes through `terragrunt run -- state pull|push` with each unit's own credentials.
- Verification is done on **plan JSON**: resource actions must be only no-op or read; output changes are ignored.
- History is per unit, next to the state. `VersionId`s are recorded for exact rollback.
- Two strategies: `surgery` (fast, verified) and `native` (`import` + `removed` blocks for PR-based flows).
- tfmigrate is not imported as a library (it shells out and isn't a stable API). The tfautomv engine (Apache-2.0) may be imported for `move suggest`.

## Consequences
- tfmigrate remains the better choice for single-directory refactors. Our docs say so honestly.
