# Spec: `tfcensus move` (v0.4)

Resource moves between Terragrunt units and accounts. tfmigrate-compatible in spirit; fixes its known pain points:
- #200: history is global, so per-unit histories are needed.
- #220: no way to mark a migration as applied.
- #221/#105: output-only diffs fail the no-op check.
- #217: only one migration per file.
- terragrunt #6350: no partial moves between units.

## Migration file (HCL)

```hcl
migration "units" "split_network" {
  from = "live/prod/eu-west-1/network"
  to = {
    "live/prod/eu-west-1/network-sg" = ["aws_security_group.app", "aws_vpc_security_group_ingress_rule.app[*]"]
    "live/prod/eu-west-1/network-endpoints" = ["module.endpoints"]
  }
  strategy = "surgery"            # surgery | native
}

migration "units" "rename_in_place" {
  from    = "live/prod/eu-west-1/eks"
  actions = ["mv module.eks_old module.eks", "rm aws_iam_role.legacy"]   # tfmigrate action syntax incl. xmv
}
```

Multiple blocks per file run in order. Glob/`xmv` semantics follow tfmigrate.

## Strategies
**surgery** (default):
1. `terragrunt run -- state pull` for each involved unit, using that unit's own credentials and role. This works across accounts without holding both credential sets at once.
2. Move resource objects in Go:
   - rewrite `provider[...]` addresses to the destination's provider config;
   - bump `serial`; keep each state's own `lineage`;
   - refuse data sources (fixes tfmigrate #159).
3. **Verify:** write the modified states to the unit cache dirs with a `_tfcensus_override.tf` local backend, run `plan -json` in every unit, and require that resource changes are only `no-op`/`read`. **Output changes are ignored** (fixes #221). A cross-account mistake shows up as a create and fails here.
4. Push the destination(s) first, then the source. Record each pre-push S3 `VersionId` in history, so `move rollback` restores exact versions. This needs bucket versioning: S3-VER must pass.

**native:**
- Generate `import` blocks (identity or id) in the destination units and `removed { from = … lifecycle { destroy = false } }` in the source.
- The user applies them through their normal PR or CI flow (Atlantis/Digger friendly). tfcensus only verifies with plans.

## History, status and safety
- History lives **per unit**, next to its state: `<key>.tfcensus-migrations.json`. It holds `{file, block, applied_at, by, version_ids}`.
- `move status <file>` shows applied or pending per unit. `move mark-applied` is for migrations done by hand.
- `move apply` requires an interactive "type the destination unit path" confirmation or `--yes` ([engineering ADR-0007](../../engineering/adr/0007-interactive-prompts.md)). It never runs over MCP.
- `move suggest` runs plans in the involved units, then uses the tfautomv engine (strict 1:1 attribute match, `--ignore` rules) to draft a migration file for review.

## Decisions (M9, 2026-11-12)
Made with the owner before coding. State I/O, rollback and confirmation are in
[ADR-0011](../adr/0011-move-state-io-and-rollback.md), which amends ADR-0006.
- File format: only `migration "units"` blocks, several per file, run in order. `actions` follow
  tfmigrate v0.4.6 exactly: shell-word splitting, `mv`, `xmv` (each `*` is a greedy group, the
  first unanchored match in each address is a source, `$1` fills the destination; the braced form
  is written `$${1}` because HCL reads `${` as a template), `rm`. `import` and `replace-provider` are rejected; use the native
  strategy for imports. A `to` entry is an address or a pattern with `*` that must match a whole
  instance address. A module step or resource without a key matches every instance. `env.NAME` and
  `var.NAME` (`--var NAME=VALUE`) can be used anywhere, so one file serves env1 and env2.
  tfmigrate's `state` and `multi_state` blocks and `.tfmigrate.hcl` aren't read.
- Before a write: the pull is `s3:GetObject` with the unit's backend credentials, so the body and
  its VersionId come from one response. Before the first push, every unit's HeadObject must still
  show the VersionId of its pull, and it is checked again right before each push. The push is
  `terragrunt run --tf-path <tf> -- state push <file>` without `-force`: terraform takes the lock
  and refuses another lineage or an older serial.
- Backups and rollback: S3 versions only, no local copies of states (verify's temp copies are 0600
  and deleted). Apply needs bucket versioning (MV-VERSIONING) and refuses a state read without a
  VersionId. Rollback takes the backend's lock, copies the pre-push version back byte for byte,
  fixes the DynamoDB digest, deletes states that didn't exist before, and refuses a state that
  changed after the push unless `--force`. Records made by `mark-applied` can't be rolled back.
- Confirmation: per block, type its last destination unit path (`from` for an in-place block);
  `rollback` and `mark-applied` ask the same way. `--yes` skips it; without a terminal and without
  `--yes` the exit code is 2.
- Cross-account: each unit uses its own backend credentials and terragrunt configuration. Before
  planning, a moved resource whose ARN names another account than the destination's discovered
  target account is MV-ACCOUNT.
- Verify: plans run one unit at a time, each with its own `TF_DATA_DIR` and `TG_DOWNLOAD_DIR`, and
  the unit's `.terraform.lock.hcl` is restored afterwards. A leftover `_tfcensus_override.tf` stops
  the run instead of being deleted. Units whose `remote_state` lacks `generate` can't be verified.
- Native: `move plan` lists the files; `move apply` writes `moves_tfcensus_<block>.tf` (import by
  identity when the state has one, else by `id`; `removed` blocks with `destroy = false`), then
  plans each unit: destinations may only import with no changes, the source may only forget. It
  writes no state and no history; `mark-applied` records it. A move of some instances of a
  resource, or of resources in keyed module instances, needs surgery.
- History: `<key>.tfcensus-migrations.json` holds `{version, unit, records[]}`, each record
  `{file, block, role, strategy, applied_at, by, version_ids{before, after}, manual,
  rolled_back_at}`. `file` is the migration file's base name. A block is applied, pending or partial
  (some units only); apply refuses a partial block. History writes are conditional on the ETag.
- Exit codes: 3 when verification rejects a block, 2 for usage errors and a refused confirmation,
  4 when credentials fail.
- Scope: `move suggest` (tfautomv) is deferred to a later milestone, so M9 adds no tfautomv
  dependency. Only the default workspace of s3 backends is supported.

## Acceptance criteria
- [x] Unit tests for state surgery on fixture states (provider rewrite, serial, lineage, module paths, count/for_each keys). `TestSpec_move_1`, `TestSurgeryActions`, `TestSurgeryErrors`.
- [x] An e2e against moto (nightly, two emulated accounts via AssumeRole, versioned buckets): split a unit into two across two accounts with surgery, verify, apply, then roll back. States are byte-compared with the originals after rollback. `TestSpec_move_2` (`mise run test:move-e2e`, nightly in `live.yml`).
- [x] The verify step rejects a move whose destination provider targets the wrong account (fixture). `TestSpec_move_3`, `TestMoveRejected`.
- [x] A per-unit history allows the same migration file to apply to env1 and env2 independently (tfmigrate #200 scenario). `TestSpec_move_4`.
