# tfcensus ADR-0004: Match by identity, then ARN, then a normalized ID; data-driven typemap

- Status: accepted (2026-11-09)

## Context
- driftctl matched by type + ID with ~100 hand-written enumerators and middlewares. That's high maintenance.
- Terraform 1.12 added resource identity: 578 of 1,761 aws provider resources have it as of 6.72.
- Most AWS resources expose an `arn` attribute.
- awscc publishes an exact awscc↔CFN mapping (`all_schemas.hcl`, MPL-2.0).

## Decision
- Matching order: identity → arn → per-type normalized ID. Synthetic IDs (`sgrule-…`) are matched by expanding the tuple.
- The mapping between TF types, CFN types, ARN patterns and ID normalizers lives in **YAML data** (`internal/typemap/data`), generated from provider schemas + awscc + curated pairs, and reviewed in PRs.
- Unmapped types are never assumed managed. They're `undetermined`, and counted visibly.

## Consequences
- Adding a type is a data PR plus a fixture, not new Go code (except native enumerators).
- Coverage grows naturally as the provider adds identity.
