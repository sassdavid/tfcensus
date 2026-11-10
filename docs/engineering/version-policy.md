# Supported versions of external tools

tfcensus is a brand-new tool with no existing users, so **we don't chase old versions**. There's one exception:
reading state and plan **files** is version-agnostic, so it costs nothing to accept older Terraform there. Features
that need newer tools are **gated** by detected version (reported as "unavailable: needs ≥X"), never crashes.
This was researched on 2026-11-09 from release notes, CHANGELOGs, upstream support policies and comparable tools'
CI matrices.

## The policy at a glance

| Tool | Hard floor | Gated features | CI test matrix | Raise the floor when |
|---|---|---|---|---|
| **Go** (building) | `go 1.26.0` directive (no patch pin, so `go install` works on both supported Go minors); release builds use toolchain **1.27.3** | — | 1.26 + 1.27 | Go 1.28 ships (~Feb 2027) → `go 1.27.0` |
| **terragrunt** | **1.0.0** | none today. Pass `--dependency-fetch-output-from-state` explicitly so 1.0, 1.1 and 1.2 behave the same | latest patch of each 1.x minor from the last 12 months (today 1.0.x, 1.1.x, 1.2.1) | only when we need a newer feature (1.x promises no breaking changes) |
| **terraform** | **1.5.0** for exec features. State files of any v4 format (≥0.12) are readable | `removed`/import `for_each` ≥1.7 · `use_lockfile` ≥1.10 (GA 1.11) · identity ≥1.12 · `query` ≥1.14 | 1.5.7 (last MPL release, many shops pinned there) + the latest two minors (today 1.16, 1.17) | when 1.5.x usage looks negligible (re-check yearly) |
| **opentofu** | **1.6.0** | `removed`/import `for_each` ≥1.7 · `removed` lifecycle and `use_lockfile` ≥1.10 · identity ≥1.12 · no `query`. Encrypted state is read only via `tofu show -json` | 1.6.x + the latest two supported (today 1.12, 1.13) | same as terraform |
| **aws provider** (data, not exec) | — | identity is detected from `resource_identity_schemas` in the schema, **not** by provider version (identity shipped quietly in 6.0, per-resource from 6.5; list resources from 6.14). v5 is treated as identity-less, falling back to arn/id matching | typemap generated from the current 6.x | — |

## Rules for implementation
- **Detect once per run:** `terragrunt --version`, `terraform version -json` (TF ≥1.17 also has `format_version`), `tofu version -json`. Cache the result in `Deps`, and have `doctor` print it with each gated feature's status.
- **Parse JSON leniently:** branch on the `format_version` **major** (state JSON 1.x, plan JSON 1.x) and ignore unknown fields. Reject an unknown major with a clear message.
- **Terragrunt `render` and `info print` aren't covered** by Terragrunt's 1.0 compatibility guarantees, so parse them leniently and keep the fast-path evaluator as the fallback ([tfcensus ADR-0001](../plan/adr/0001-shell-out-to-terragrunt.md)). `find` JSON and the run-report schema *are* guaranteed (fields only get added).
- **Never pass `--hidden`.** It's the default since Terragrunt 1.0 and the flag is a deprecated no-op.
- Patch numbers aren't contiguous (OpenTofu skipped 1.13.3–1.13.7). Compare versions, never count them.
- Terragrunt's own tested Terraform list stops at 1.14.x. Our CI covers 1.16 and 1.17 with Terragrunt 1.2.x ourselves.

## Why not support more
- **Upstream support:** Terraform patches only the latest two minors. OpenTofu supports about two lines (1.12 until 2027-02, 1.13 until 2027-08). Terragrunt patches only the latest minor.
- **Comparable tools:** tfmigrate tests floor + latest two (TF 0.12.31/1.16/1.17, Tofu 1.6.3/1.12/1.13); driftive claims TF ≥0.14, Terragrunt ≥0.73.
- **Terragrunt below 1.0** had several removal waves (`--terragrunt-*` flags removed in 0.85, legacy commands in 0.90), and supporting them would mean two discovery code paths for no users.
