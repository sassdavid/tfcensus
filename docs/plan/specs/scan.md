# Spec: `tfcensus scan` report

## Flow
capability probe → discovery (F1) → state index (F3) → inventory layers → match → CloudFormation stacks → ownership rules → report.

## Categories

| Category | Meaning | Counts toward `--fail-on unmanaged` |
|---|---|---|
| managed | matched to exactly one state resource | no |
| double-managed | matched in more than one unit | separate gate `double-managed` |
| unmanaged | no match, no rule | **yes** |
| other-iac | owned by CFN/CDK/Pulumi/k8s controllers: listed by a CloudFormation stack, or tagged | no |
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
- Coverage KPI: `(managed + double-managed) / (managed + double-managed + unmanaged)` per account
  and per region, in percent, or `-` when the denominator is 0. A double-managed resource is in
  state, so it counts as managed. This is the IaC coverage number teams track over time.
- Exit codes: 3 when `--fail-on unmanaged|double-managed` finds at least `--min-count` findings
  (scoped by `--types`); else 1 when a layer, a state read or discovery failed; 4 when every
  failure was a credential problem and no layer listed anything; 0 otherwise.
- CloudFormation source ([ADR-0014](../adr/0014-cloudformation-stack-source.md)): with
  `scan.cloudformation` on (the default), scan lists every stack's resources. A resource that a
  stack lists and no state manages is `other-iac` with owner `cloudformation` and rule
  `source/cloudformation`, before any ownership rule. Every finding a stack lists has a `stack`
  object (`name`, `id`, `root` for a nested stack, `logical_id`). A managed or double-managed one
  keeps its category and the report warns. CSV has a `stack` column; tty and md list the stacks.
- Inheritance ([ADR-0017](../adr/0017-scan-accuracy.md)): a second pass gives an `unmanaged` or
  `undetermined` child (policy attachments and inline policies, bucket subresources, security
  group rules, apex NS and SOA records, a regional NAT gateway's route table, a `CHILD` VPC
  Lattice resource configuration) the category of an `aws-managed`, `other-iac` or `derived`
  parent, with rule `inherit/<category>` and a `parent` object. A child of a `managed` parent stays
  `unmanaged`. The pass repeats up to depth 3, and `scan.never_hide` stops it at guarded resources.
- `--explain <arn|id>` prints one resource's match, every rule's outcome, its parent chain and its category;
  `--explain-coverage` prints the layer coverage matrix, the records a layer left out, and the
  AWS Config types scan doesn't map with their counts (one `config:GetDiscoveredResourceCounts`
  per recording account and region, skipped when `--types` narrows the scan). A re-run with other rules reads the cached
  inventory ([ADR-0009](../adr/0009-scan-snapshot-cache.md)).

## Acceptance criteria
- [x] An end-to-end testscript with stubbed AWS + fixture states covers all categories and all formats (goldens).
- [x] The summary's coverage % excludes aws-managed, derived, other-iac, ignored and undetermined (documented formula, unit-tested).
- [x] `--fail-on unmanaged --types 'AWS::IAM::*'` exits 3 only for IAM findings.
- [x] A re-run with different `--rules` uses the cache (no AWS calls; counter).
- [x] CloudFormation stack resources (nested stacks, paginated listings, retained and deleted
  resources, a denied region) join live resources by ID, ARN, name or ARN segment. A stack decides
  before the rules, a state before the stack, and `scan.cloudformation: false` makes no stack
  calls (`TestStacks`, `TestStackIndex`, `TestClassifyStacks`, `TestScanCloudFormationOff`, the
  `spec_scan_1` goldens).
