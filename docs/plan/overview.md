# tfcensus: overview

> A census of your cloud against your IaC: what each Terraform/OpenTofu state manages, what nothing manages,
> and how healthy the state backends are, across every account and every Terragrunt unit.

## Problem
1. **ClickOps goes unseen.** Resources created by hand (or by a forgotten script) aren't in any state.
   - **driftctl**, the reference OSS tool, has been in maintenance mode since 2023-06 (last release v0.40.0, 2023-12).
   - It broke on AWS provider 5.x, because it executes a pinned provider binary (3.19) and imports Terraform 0.14 internals.
   - No fork is maintained. The rest are either stale (cloud-concierge, terracognita), archived (Terraformer), or commercial (Firefly, ControlMonkey, Snyk).
2. **Fleet-wide visibility needs scripts.** With dozens of Terragrunt repos, dozens of AWS profiles and hundreds of units, the obvious questions have no tool:
   - Which units exist, in which account, on which Terraform and provider versions?
   - Which unit manages this ARN?
   - Is anything managed twice?
3. **Backend hygiene is unchecked.** State buckets need versioning, replication, KMS and no orphaned keys.
   - Prowler checks buckets generically, but it doesn't know which buckets hold state.
   - Real work this year included migrating state backends with replication across waves of accounts.
4. **State refactoring across units is manual surgery.** Moving resources between Terragrunt units or accounts is done by hand: SG/EKS/MSK migrations with hand-written import/`state mv` runbooks.
   - tfmigrate is per-directory, with global history (issue #200).
   - `terragrunt backend migrate` moves whole states only (#6350 is still open).

## Users
- Platform teams running Terraform/OpenTofu with Terragrunt on AWS, from a few accounts up to an Organization.
- Security and FinOps reviewers who want "unmanaged" and "double-managed" lists.
- CI pipelines (scheduled census and drift reports) and LLM agents via MCP (read-only).

## Goals
- **G1 Units.** Discover every unit (Terragrunt, or plain Terraform root modules) and resolve its backend, account, region and credentials.
- **G2 Inventory.** Read all states directly from S3 (read-only, no locks): resource counts, Terraform and provider versions, last modified, lineage.
- **G3 Scan.** Find live AWS resources not managed by any state. Classify each as unmanaged / other-IaC / AWS-managed / derived / ignored / undetermined, with a reason.
- **G4 Audit.** Check state backends: S3 and lock-table hygiene, plus fleet checks (orphaned keys, stale locks, duplicate lineage, double-managed resources).
- **G5 Drift (v0.2).** Fleet drift via `terragrunt run --all` refresh-only plans. Collapse identical changes across units, noise rules with expiry, PR-ready reports.
- **G6 Whois and codify (v0.3).** Reverse lookup (ARN → unit). Generate `import` blocks for unmanaged resources into a chosen unit.
- **G7 Move (v0.4).** tfmigrate-compatible migrations between units and accounts, with plan-verified safety, history per unit and rollback.
- **G8 Integrations (v0.5).** Slack, GitHub and Bitbucket issue lifecycle, static HTML report, MCP.

## Non-goals
- PR plan/apply orchestration, locking, approvals (Atlantis, Digger, Terramate, Spacelift).
- Wrapping `apply`, replacing Terragrunt's DAG/scheduler, or a hosted dashboard or database (Terraboard, Driftive Cloud).
- A cost engine (infracost), a policy engine (OPA/conftest), or generating full HCL resource config (`terraform query -generate-config-out` does that; we emit imports and `.tfquery.hcl`).
- Non-AWS clouds and non-S3 backends before 1.0. The interfaces must not preclude them.

## Success criteria (v0.1)
- On the author's fleet (read-only):
  - `tfcensus units` finds every unit in the author's Terragrunt repos.
  - `tfcensus inventory` reads every S3 state in under 60 s.
- `tfcensus scan` on one non-prod account:
  - false-positive rate under 5% after built-in ownership rules (validated by hand on one non-prod account);
  - every finding has a reason.
- `tfcensus audit backend` reproduces known facts (e.g. which state buckets already have replication after the 2026 waves).
- Zero writes to cloud or state, proven by IAM: the documented read-only policy is sufficient for all v0.1 commands.
