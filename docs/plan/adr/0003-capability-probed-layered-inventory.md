# tfcensus ADR-0003: Layered inventory, chosen per account/region by a capability probe

- Status: accepted (2026-11-09)

## Context
No single AWS API lists everything:
- Resource Explorer: ~649 types live, free, ARN-native, but needs a view and lags.
- Config: ~718 types with properties and relationships, but only where a recorder runs.
- Cloud Control: ~1,234 listable types, but slow, throttled, and 439 types need parents.

A live probe of a non-prod Control Tower member account (2026-11-09) showed **LOCAL Resource Explorer indexes only** and a **Control Tower Config recorder (CONTINUOUS, allSupported)** in the governed regions, with none in us-east-1. So a fixed "Resource Explorer first" order would be wrong for Control Tower estates.

## Decision
- `doctor` and `scan` probe each (account, region): RE index type and view, Config recorder status, aggregator availability. The result is cached 24 h.
- Default `--layers auto`: **config** (when recording) → **resource-explorer** (when a view exists) → **native** enumerators (always, for the awkward types) → **cloudcontrol** only for allowlisted gap types.
- Org-wide fast path: if a Config aggregator is configured (Control Tower Audit account), use `SelectAggregateResourceConfig` once instead of per-account calls.
- We never create indexes, views or recorders (read-only). `doctor` prints exactly what an admin could enable and why.
- A coverage matrix (`--explain-coverage`) makes blind spots explicit per type, account and region.

## Consequences
- More code paths, but each layer is a small `Layer` implementation with stubbed tests.
- Results differ between estates by design, and the report always states which layers ran.
