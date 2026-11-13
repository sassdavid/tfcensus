# Spec: `tfcensus drift`

Takes tgsieve's ingestion model and driftive's issue lifecycle (via `notify`), on top of F1/F2.

## Running
- `terragrunt run --all --filter <…> --tf-path <bin> --json-out-dir <tmp> --report-file <tmp>/report.json --report-format json --log-format json -- plan -refresh-only -lock=false -detailed-exitcode`.
  - `--json-out-dir` writes `<unit>/tfplan.json` per unit. `run --all -- plan -json` is unusable because parallel output interleaves.
  - NDJSON logs drive live progress (unit started/finished) on stderr.
- `--mode refresh-only` (default; pure drift) or `--mode full` (drift that this plan reverts vs drift that it leaves, the tgsieve split).
- Or ingest existing artifacts: `tfcensus drift --from-json-out-dir <dir> --report-file <file>`, so CI that already runs plans doesn't re-run them.

Decisions made at M7 (checked against terragrunt v1.2.2):
- Run mode discovers units as `units` does (filters, `--generate-stacks`, `--render`) and runs
  terragrunt once per repo with terragrunt units, in the symlink-resolved repo directory, with
  `TG_TF_PATH` set. It adds `--no-color`. Plain root modules are skipped with a warning.
- Plans go to a temporary directory with mode 0700, removed when drift ends.
- A unit starts with its first log line and finishes with terragrunt's `show -json` for its plan.
  Log messages are never logged, because terraform output can hold attribute values.
- terragrunt exits 2 for changes and 1 for a failed unit. Both leave a report, so only a missing
  report is a repo error.
- The run report's `Name` is the unit path relative to the run's working directory, and plans are
  at `<json-out-dir>/<Name>/tfplan.json`. In run mode the repo path is prefixed to both.

## Analysis
- Parse each plan with terraform-json (`ResourceDrift`, `ResourceChanges`, `OutputChanges`), tolerating missing newer fields (OpenTofu).
- Attribute-level diff honouring `replace_paths`, sensitive values and unknowns.
- **Collapse across units:** identical changes (same type, same attribute diff shape) across N units become one group with N members (`--collapse shape|strict|none`).
- **Noise rules** (`drift.noise:` in config): selectors (unit glob, type, address, attribute path, action) with `expires` and `reason`. Presets: `aws-tags-all`, `computed-hashes`. `never_hide: [delete, replace]`.
- The run report gives per-unit result (succeeded/failed/excluded/early exit). Failed units become report errors, never silent.

Decisions made at M7:
- Drift is `resource_drift` (managed resources only). Paths look like `tags_all.Owner`,
  `ingress[0].cidr_blocks` or `tags["kubernetes.io/role"]`. A node that is sensitive or unknown on
  either side is one path, and nothing below it is listed.
- In full mode a drifted resource is reverted when the plan's change for the same address is a
  create, delete or replace, or an update that touches a drifted path. Planned changes that revert
  no drift count as `pending`. Attributes under a `replace_paths` entry are marked.
- `shape` compares type, action, revert and the sorted visible paths, with list indices as `[*]`.
  `strict` also compares kinds and before and after values, in memory only. `none` makes one group
  per change.
- Output shows paths and kinds (added, removed, changed) only. `--show-attributes` adds before and
  after values. Sensitive values show as `(sensitive)` and unknown ones as `(known after apply)`,
  with or without the flag.
- Noise rule selectors are ANDed. Globs use `*` for any characters, `/` included. `attribute`
  matches the path and everything below it; `[*]` matches any index. A rule with an attribute
  hides only the matching attribute changes, and a change left with none is hidden. A rule without
  one hides the whole change. The first rule that matches wins.
- Presets are opt-in list entries (`- preset: aws-tags-all`) and take no other key.
  `aws-tags-all` hides updates to `tags_all` on `aws_*` types. `computed-hashes` hides updates to
  `source_code_hash`, `etag`, `last_modified`, `output_md5`, `output_sha`, `output_sha256`,
  `output_sha512`, `output_base64sha256`, `output_base64sha512` and `content_md5`.
- `expires` and its warning work as for ownership rules (`ownership.ParseExpires`). never_hide
  applies to every noise rule, presets included, and checks both the drift action and the revert.
  A change it keeps records which rule didn't apply.

## Output and gates
- tty / md (cut at `--md-max-chars`, 60,000 characters by default, `<details>` per group) / json (`schemas/drift-v1.json`) / GitHub workflow annotations / sarif.
- `--fail-on drift` (any), `drift:reverts` (only drift the current code would revert), `errors`.
- Exit codes follow the shared contract (3 when matched). Terraform's own `-detailed-exitcode` is aggregated internally.

Decisions made at M7:
- Annotations are `--format github`: one error per failed unit or repo, one warning per group
  (at most 10 members named), one per report warning and a closing notice.
- The md report starts with the summary, errors and warnings, so truncation only drops groups.
- SARIF has one result per change, with hidden changes as suppressed results, and one per failed
  unit.
- `--fail-on` takes a list. `drift` counts visible drifted resources, `drift:reverts` the visible
  ones the plan reverts (a usage error without `--mode full`), and `errors` failed units and repos.
  `--min-count` applies to each gate. Exit 3 when any gate reaches it, else 1 when a unit or repo
  failed, else 0. Early exit and excluded units aren't errors.
- [`docs/ci/drift.yml`](../../ci/drift.yml) is the example workflow: one terragrunt run, then
  annotations, the step summary and SARIF from the same plans, and the gate last.

## Acceptance criteria
- [x] Ingest-mode tests from recorded `json-out-dir` + report fixtures (12 units, 3 identical tag drifts → 1 collapsed group, 1 failed unit).
- [x] Noise rule with an expired date stops hiding and warns. `never_hide` is enforced.
- [x] Run mode is verified with a fake terragrunt that writes fixture plans (argv asserted).
- [x] Markdown output under 60 KB for a 300-unit fixture with a truncation footer. Since M13 the
  cap is 60,000 characters (`--md-max-chars`, [engineering ADR-0014](../../engineering/adr/0014-report-outputs-and-pr-comment.md));
  `TestSpec_drift_4` checks it.
