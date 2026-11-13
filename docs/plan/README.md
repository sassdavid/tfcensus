# tfcensus docs

**Status:** draft plan, 2026-11-09. Not started. Language: Go ([ADR-0001](../engineering/adr/0001-language-go.md)).

`tfcensus` takes a census of your AWS estate against your Terraform/OpenTofu + Terragrunt fleet. It is the
open-source successor to driftctl's unmanaged-resource detection, rebuilt to be:
- multi-account and Terragrunt-aware;
- identity-based (no provider binaries);
- combined with fleet inventory, state-backend audit, drift reports, ARN→unit lookup, import codification,
  and cross-unit/account state moves.

Contributor and agent rules: [CLAUDE.md](../../CLAUDE.md).

## Plan (`docs/plan/`)

Reading order:
1. [overview.md](overview.md): problem, users, goals, non-goals, success criteria
2. [architecture.md](architecture.md): foundations F1–F4, packages, data model
3. [cli.md](cli.md): commands, selection flags, gates
4. Specs, foundations first:
   [discovery](specs/discovery.md) ·
   [accounts-and-credentials](specs/accounts-and-credentials.md) ·
   [state-reader](specs/state-reader.md) ·
   [inventory](specs/inventory.md) ·
   [matching](specs/matching.md) ·
   [ownership-rules](specs/ownership-rules.md) ·
   [scan](specs/scan.md) ·
   [backend-audit](specs/backend-audit.md) ·
   [drift](specs/drift.md) ·
   [whois-and-codify](specs/whois-and-codify.md) ·
   [move](specs/move.md) ·
   [notifications-and-reports](specs/notifications-and-reports.md) ·
   [config](specs/config.md)
5. [milestones.md](milestones.md): M0-M20. v0.1.0 is M0-M11; the release mapping is at the top
6. [testing.md](testing.md) · [risks.md](risks.md) · [open-questions.md](open-questions.md) · [research.md](research.md)
7. Tool ADRs:
   [0001 shell out](adr/0001-shell-out-to-terragrunt.md) ·
   [0002 raw state, no provider binaries](adr/0002-raw-state-no-provider-binaries.md) ·
   [0003 capability-probed layers](adr/0003-capability-probed-layered-inventory.md) ·
   [0004 identity/arn/id matching](adr/0004-identity-arn-id-matching.md) ·
   [0005 CEL rules](adr/0005-cel-ownership-rules.md) ·
   [0006 tfmigrate-compatible move](adr/0006-tfmigrate-compatible-move.md) ·
   [0007 read-only by default](adr/0007-read-only-by-default.md) ·
   [0008 audit suppression fields](adr/0008-audit-suppressions-fields.md) ·
   [0009 scan snapshot cache](adr/0009-scan-snapshot-cache.md) ·
   [0010 rules on undetermined, guards](adr/0010-rules-on-undetermined-and-guards.md)
8. [reference-repos.md](reference-repos.md): local reference clones and their licenses

## Engineering (`docs/engineering/`)

Stack, standards and release pipeline, shared in spirit with the sibling tool
[relgap](https://github.com/sassdavid/relgap): [engineering/README.md](../engineering/README.md).
- [engineering-standards.md](../engineering/engineering-standards.md) ·
  [stack.md](../engineering/stack.md) ·
  [ci-and-release.md](../engineering/ci-and-release.md) ·
  [version-policy.md](../engineering/version-policy.md)
- Engineering ADRs: [docs/engineering/adr/](../engineering/README.md#decisions-adrs)

## Integration
How tfcensus connects to relgap: [integration.md](integration.md).
