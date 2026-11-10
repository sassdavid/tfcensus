# ADR-0001: Go for both tools

- Status: accepted (2026-11-09)
- Applies to: relgap, tfcensus

## Context
The author started out preferring Rust but was open to Go. We compared both for each tool, using the
dependency manifests of about 40 maintained CLIs (jdx/mise, uv, ruff, ripgrep, ccusage on the Rust side; gh,
terragrunt, opentofu, tfmigrate, trivy, aqua, goreleaser on the Go side).

## Decision
Both tools are written in **Go**.

## Reasons
**tfcensus (clear):**
- The libraries it needs exist only in Go: `terraform-json`, `terraform-exec`, `hcl/v2` with full expression evaluation and `hclwrite`, `go-cty`.
- In Rust, `hcl-rs` parses HCL but has no Terraform function library and no tfexec or plan-JSON equivalent.
- Every tool in this space is Go: terragrunt, opentofu, tfmigrate, tfautomv, atlantis, terramate, driftive, tgsieve. So the contributor pool already knows the language.
- `aws-sdk-go-v2` has far more examples, and SSO/`sso-session` behaviour is proven at scale (steampipe, trivy).

**Both tools:**
- One language means one stack, one lint/CI/release template, and shared conventions between the two tools.
- Fast compiles shorten the edit-test loop, which matters when Claude Code does most of the implementation.

relgap's tool-specific reasoning lives in its own copy of this ADR
([sassdavid/relgap](https://github.com/sassdavid/relgap/blob/main/docs/engineering/adr/0001-language-go.md)).

## Consequences
- Binaries are larger than Rust equivalents: tfcensus ≈ 40–60 MB with the AWS clients (estimate). Acceptable.
