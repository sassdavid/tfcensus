# tfcensus ADR-0007: Read-only by default; state writes only via `move apply`

- Status: accepted (2026-11-09)

## Decision
- Every command except `move apply|rollback|mark-applied` works with the documented read-only IAM policy. Tests assert that read-only code paths never call a write API (an API-call allowlist checked against recorded HTTP traffic). Validating the IAM policy on real AWS is optional and manual.
- The state reader takes no locks and never writes.
- `move apply`:
  - requires bucket versioning (S3-VER) and an interactive typed confirmation or `--yes`;
  - records VersionIds before pushing;
  - logs every write at info level;
  - is never exposed via MCP.
- `doctor` never enables Resource Explorer, Config or anything else. It only explains what's missing.

## Reasons
- Security teams must be able to run the tool with `ReadOnlyAccess` + `ViewOnlyAccess`.
- Trust: a census tool that can change infrastructure is a much harder sell.
