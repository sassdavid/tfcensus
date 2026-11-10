# tfcensus ADR-0002: Read raw state JSON; never execute provider binaries for scanning

- Status: accepted (2026-11-09)

## Context
driftctl downloaded `terraform-provider-aws` (default 3.19.0) and decoded state through provider schemas over
go-plugin gRPC. That broke on aws 5.x (#1671, #1682, #1683) and made every provider release a compatibility event.

## Decision
- Parse **raw state v4** with our own minimal struct (identifiers only, streamed).
- Identity, `arn` and `id` are enough for matching ([matching.md](../specs/matching.md)). We never need full schemas at scan time.
- Provider schema knowledge (identity schemas, ID formats) is captured **offline** into typemap data by a generation task. It's versioned with `min/max_provider_version` where formats changed.
- Live inventory uses AWS APIs directly (aws-sdk-go-v2), never the provider.

## Consequences
- A new provider release can't break scanning. At worst, new types show up as `undetermined` until typemap catches up.
- Scanning works without terraform installed (only discovery and drift need binaries).
