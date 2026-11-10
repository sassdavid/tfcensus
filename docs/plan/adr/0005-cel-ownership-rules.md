# tfcensus ADR-0005: Ownership and noise rules use CEL, first-match, with expiry

- Status: accepted (2026-11-09)

## Context
False positives killed trust in driftctl (#1548, #1687, #530). It had `.driftignore` globs plus JMESPath filters,
and no idea of "owned by CloudFormation" (#878). tgsieve shows good patterns: `expires`, `never_hide`, presets, `--explain`.

## Decision
- Rules are ordered and first-match. Each produces `{category, owner, reason, rule}`. Built-in packs come first, then user rules.
- Expressions use **CEL** (google/cel-go): typed, sandboxed, fast, familiar from Kubernetes. We considered JMESPath (no typing, poor errors) and OPA/Rego (heavy, overkill).
- Every user rule can carry `expires` (fails open with a warning) and must carry `reason`. `never_hide` guards critical categories.
- `scan --explain <arn>` and `rules test` make every decision traceable.
- The same rule engine powers drift noise rules and audit suppressions, so there's one mental model.

## Consequences
- cel-go is a moderately heavy dependency, accepted for correctness and UX.
- The rule schema is versioned in `schemas/config-v1.json`.
