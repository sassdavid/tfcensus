# ADR-0003: koanf for layered configuration

- Status: accepted (2026-11-09)

## Context
- viper v1.21.0 is the most common choice (trivy, syft, k9s, goreleaser, atlantis), but it lowercases keys and pulls in many dependencies.
- gh and chezmoi hand-roll YAML.
- tfcensus needs layering of defaults < user file < project file < env < flags, with provenance (`config show --sources`).

## Decision
Use **koanf/v2** (v2.4.1). It reads config from a file, from env vars, and from cobra's flags via the `posflag` provider. Paths come from `adrg/xdg`.

## Reasons
- Case-sensitive keys and a small dependency footprint.
- An explicit load order makes provenance easy: we record which provider set each key.
- Unmarshals into typed structs. Unknown keys are caught by validating the raw map against the JSON Schema in `schemas/` before unmarshalling.

## Consequences
- The config struct is the single source of truth. The JSON Schema is generated from it with `invopop/jsonschema` (verify at M0) and checked in. A test fails if they drift apart.
- Env var mapping: `TFCENSUS_CACHE_TTL` → `cache.ttl` (`__` for nesting when a key itself has `_`). Document this in `docs/config.md`.
