# tfcensus ADR-0010: Ownership rules also classify undetermined resources; never_hide uses guards

- Status: proposed (2026-11-11)

## Context
- The ownership spec says rules evaluate every unmatched resource. Matching labels resources of
  unmapped types `undetermined` (ADR-0004), so the derived pack's rules for ECS tasks or backup
  snapshots of unmapped types would never run, and those resources would stay prominent noise.
- The spec's `never_hide: [category...]` example ("ignored can never apply to IAM users with console
  access") needs a notion of which resources are protected. AWS Config doesn't record whether an
  IAM user has a console password.

## Decision
- Rules run for `unmatched` and `undetermined` results. A matching rule sets aws-managed, derived,
  other-iac or ignored. Without a match, unmatched becomes unmanaged and undetermined stays
  undetermined. No rule can make a resource managed, so ADR-0004 holds.
- Guards are built-in CEL expressions (`internal/ownership/packs/guards.yaml`). The first guard is
  every IAM user. A user rule whose category is in `never_hide` is skipped for a guarded resource,
  and the `--explain` trace says so. Built-in packs aren't affected.
- Packs are CEL too, embedded as YAML, so `--explain` and `rules test` show one trace format for
  every rule.

## Consequences
- Fewer undetermined resources once packs cover them, with the rule recorded on each finding.
- Guards are fixed for now. Making them configurable is a later, additive change.
