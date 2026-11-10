# tfcensus: milestones

**One milestone = one fresh Claude Code session.** Start each session with:
"Read `CLAUDE.md`, then `docs/plan/milestones.md` M<n> and the linked specs. Implement M<n> only."
Tick a box only when its check has passed, and quote the evidence in the commit body.
Never run anything against real AWS accounts in a session unless the prompt explicitly allows a named read-only profile.

Release mapping: **v0.1.0 = M0–M6**, v0.2 = M7, v0.3 = M8, v0.4 = M9, v0.5 = M10.

---

### M0: Skeleton and pipeline
Same as [relgap M0](https://github.com/sassdavid/relgap/blob/main/docs/plan/milestones.md) (the shared template in [docs/engineering](../engineering/README.md)), plus `execx` with a fake-binary test harness, plus `docs/iam/tfcensus-readonly.json`.
- [x] `mise run check` is green. Snapshot release builds. CI is green (linux, macos and windows).
- [x] `tfcensus version --format json` validates against the envelope schema.

### M1: Discovery (F1) → [discovery](specs/discovery.md)
- [x] **Spike:** check whether `terragrunt render --json` (v1.2.1) includes `remote_state`. Record the answer and the chosen approach in [adr/0001](adr/0001-shell-out-to-terragrunt.md).
- [x] All discovery.md acceptance criteria. `tfcensus units` on fixtures, in tty and json.

### M2: Accounts and credentials (F2) → [accounts](specs/accounts-and-credentials.md)
- [ ] Profile/SSO loading, Org mode, backend role chains, the expired-SSO UX. `tfcensus doctor` with the capability probe (stubbed).
- [ ] *(Optional, manual, read-only on an existing work account, if employer policy allows)* `tfcensus doctor --profile <nonprod>` matches the facts recorded in the spec.

### M3: State reader and inventory (F3) → [state-reader](specs/state-reader.md)
- [ ] All state-reader.md criteria. `tfcensus inventory` in json, md and csv. `--save-index`.

### M4: Backend audit → [backend-audit](specs/backend-audit.md)
- [ ] Bucket, lock and fleet checks with fixtures. Suppressions. SARIF output.
- [ ] *(Optional, manual)* Live validation recorded in `docs/validation/` (read-only, profiles named in the prompt).

### M5: Inventory layers and matching → [inventory](specs/inventory.md), [matching](specs/matching.md)
Scope: config + resource-explorer + native (sg-rules, iam-attachments, route53, s3-subresources) layers; typemap generation task + the first 40 types; the match algorithm.
- [ ] inventory.md criteria 1–3, matching.md criteria 1–4.

### M6: Ownership rules and scan report → v0.1.0 ([ownership](specs/ownership-rules.md), [scan](specs/scan.md))
- [ ] All ownership-rules.md and scan.md criteria (emulator and fixture based).
- [ ] *(Optional, manual, strongly recommended before announcing v0.1)* A false-positive review on one real non-prod account, under 5% (recorded, aggregated only).
- [ ] Cloud Control layer for allowlisted types with the parent graph (inventory criterion 4).
- [ ] v0.1.0 tagged and released. Homebrew cask, `mise use github:sassdavid/tfcensus`, and the container image all work.

### M7: Drift (F4) → v0.2.0 ([drift](specs/drift.md))
- [ ] All drift.md criteria. A GitHub Actions example workflow in `docs/ci/`.

### M8: whois + codify → v0.3.0 ([whois-and-codify](specs/whois-and-codify.md))
- [ ] All criteria. FL-DOUBLE audit check shares the whois index.

### M9: move → v0.4.0 ([move](specs/move.md))
- [ ] All move.md criteria, including the cross-account e2e against moto (two emulated accounts via AssumeRole) with rollback.
- [ ] `docs/runbooks/move.md`: the runbook a colleague can follow (generalizes the author's earlier hand-written migration runbooks).

### M10: Notifications, HTML, MCP → v0.5.0 ([notifications-and-reports](specs/notifications-and-reports.md))
- [ ] All criteria. The MCP server is registered in the author's Claude Code and used read-only for a real whois or scan question, recorded in `docs/dogfood.md`.
