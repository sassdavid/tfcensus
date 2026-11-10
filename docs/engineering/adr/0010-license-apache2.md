# ADR-0010: Apache-2.0 license

- Status: accepted (2026-11-09)

## Decision
tfcensus is released under **Apache-2.0**.

## Reasons
- Includes a patent grant. It's the norm for DevOps tooling (driftctl, tfautomv, driftive, aws-sdk).
- It's compatible with the dependencies we plan to use:
  - MPL-2.0 libraries (terraform-json, hcl/v2, terraform-exec, terraform-config-inspect, awscc data). MPL is file-level copyleft: we import these unmodified. If we ever modify an MPL file, it stays under MPL.
  - MIT libraries (go-cty, tfmigrate design).
  - Apache-2.0 code (driftctl middleware ideas, tfautomv engine).
- Copying ideas from driftctl's middleware catalogue (Apache-2.0) is fine. If any code is adapted, keep its notice in `NOTICE`.

## Hard rules
- Never copy or import Terraform core code (BSL 1.1) or OpenTofu/terragrunt `internal/` packages.
