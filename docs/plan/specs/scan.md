# Spec: `tfcensus scan` report

## Flow
capability probe → discovery (F1) → state index (F3) → inventory layers → match → ownership rules → report.

## Categories

| Category | Meaning | Counts toward `--fail-on unmanaged` |
|---|---|---|
| managed | matched to exactly one state resource | no |
| double-managed | matched in more than one unit | separate gate `double-managed` |
| unmanaged | no match, no rule | **yes** |
| other-iac | owned by CFN/CDK/Pulumi/k8s controllers | no |
| aws-managed | AWS-created or default | no |
| derived | child of a managed or derived resource | no |
| ignored | user rule | no |
| undetermined | type unmapped, or layer data insufficient | no, but shown prominently |

## Output
- **tty:** a summary per account/region (counts per category, coverage %), then unmanaged findings grouped by type, with name, ARN, tags (owner/created-by if present), and age if Config has `resourceCreationTime`.
- **md:** the same, collapsible per account. For issues and wikis.
- **json:** the full findings array (schema `schemas/scan-v1.json`). This is also the input for `codify` and `report html`.
- **csv:** a flat list for spreadsheets (FinOps).
- **sarif:** each unmanaged/double-managed finding becomes a result. The location is the unit dir for double-managed, otherwise a synthetic `aws://account/region/type/id` URI.
- Coverage KPI: `managed / (managed + unmanaged)` per account. This is the IaC coverage number teams track over time.

## Acceptance criteria
- [ ] An end-to-end testscript with stubbed AWS + fixture states covers all categories and all formats (goldens).
- [ ] The summary's coverage % excludes aws-managed, derived, other-iac, ignored and undetermined (documented formula, unit-tested).
- [ ] `--fail-on unmanaged --types 'AWS::IAM::*'` exits 3 only for IAM findings.
- [ ] A re-run with different `--rules` uses the cache (no AWS calls; counter).
