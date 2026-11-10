# Spec: `tfcensus drift` (v0.2)

Takes tgsieve's ingestion model and driftive's issue lifecycle (via `notify`), on top of F1/F2.

## Running
- `terragrunt run --all --filter <…> --tf-path <bin> --json-out-dir <tmp> --report-file <tmp>/report.json --report-format json --log-format json -- plan -refresh-only -lock=false -detailed-exitcode`.
  - `--json-out-dir` writes `<unit>/tfplan.json` per unit. `run --all -- plan -json` is unusable because parallel output interleaves.
  - NDJSON logs drive live progress (unit started/finished) on stderr.
- `--mode refresh-only` (default; pure drift) or `--mode full` (drift that this plan reverts vs drift that it leaves, the tgsieve split).
- Or ingest existing artifacts: `tfcensus drift --from-json-out-dir <dir> --report-file <file>`, so CI that already runs plans doesn't re-run them.

## Analysis
- Parse each plan with terraform-json (`ResourceDrift`, `ResourceChanges`, `OutputChanges`), tolerating missing newer fields (OpenTofu).
- Attribute-level diff honouring `replace_paths`, sensitive values and unknowns.
- **Collapse across units:** identical changes (same type, same attribute diff shape) across N units become one group with N members (`--collapse shape|strict|none`).
- **Noise rules** (`drift.noise:` in config): selectors (unit glob, type, address, attribute path, action) with `expires` and `reason`. Presets: `aws-tags-all`, `computed-hashes`. `never_hide: [delete, replace]`.
- The run report gives per-unit result (succeeded/failed/excluded/early exit). Failed units become report errors, never silent.

## Output and gates
- tty / md (capped at 60 KB, `<details>` per group) / json (`schemas/drift-v1.json`) / GitHub workflow annotations / sarif.
- `--fail-on drift` (any), `drift:reverts` (only drift the current code would revert), `errors`.
- Exit codes follow the shared contract (3 when matched). Terraform's own `-detailed-exitcode` is aggregated internally.

## Acceptance criteria
- [ ] Ingest-mode tests from recorded `json-out-dir` + report fixtures (12 units, 3 identical tag drifts → 1 collapsed group, 1 failed unit).
- [ ] Noise rule with an expired date stops hiding and warns. `never_hide` is enforced.
- [ ] Run mode is verified with a fake terragrunt that writes fixture plans (argv asserted).
- [ ] Markdown output under 60 KB for a 300-unit fixture with a truncation footer.
