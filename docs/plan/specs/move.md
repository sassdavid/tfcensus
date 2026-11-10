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

## Acceptance criteria
- [ ] Unit tests for state surgery on fixture states (provider rewrite, serial, lineage, module paths, count/for_each keys).
- [ ] An e2e against moto (nightly, two emulated accounts via AssumeRole, versioned buckets): split a unit into two across two accounts with surgery, verify, apply, then roll back. States are byte-compared with the originals after rollback.
- [ ] The verify step rejects a move whose destination provider targets the wrong account (fixture).
- [ ] A per-unit history allows the same migration file to apply to env1 and env2 independently (tfmigrate #200 scenario).
