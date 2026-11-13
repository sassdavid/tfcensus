# Spec: `whois` and `codify`

## whois
`tfcensus whois <arn|id> [--type aws_x]…` uses the StateIndex (F3) across all discovered units:
- Output: unit(s), address, repo path, account and region, and the match key used (identity, arn or id).
- More than one unit means double-managed, shown with a warning and exit 3 under `--fail-on double-managed`.
- No match: also runs the live ownership rules for that single resource (if credentials are available) to explain why it isn't managed (e.g. "other-iac: CloudFormation stack X").
- Batch mode: `--stdin` reads ARNs, one per line (pipe from CloudTrail, Config or a scan report).
- The index can be persisted: `tfcensus inventory --save-index index.json.zst` lets whois run offline, fast, in CI.

Decisions made at M8:
- whois and scan share one lookup: `match.Matcher`. An ARN whose type the typemap maps is matched
  like a live resource (identity, then ARN, then id). Any other ARN, and a bare id, is looked up
  across every type; `--type` narrows to TF types. Hits in more than one TF type are
  `undetermined` with rule `match/ambiguous` (keys include the type, so a topic and its policy
  sharing an ARN are two resources), and the ownership rules don't run on them.
- FL-DOUBLE reads the index's `double_managed`, which `match.Index` now fills from the same
  matcher keys. `tfstate.Entry.Keys()`, the second lookup path, is gone. IDs of providers other
  than aws and awscc (`random_password`'s `none`) never form keys.
- Each answer is a scan finding, so `--fail-on unmanaged,double-managed`, `--min-count` and exit 3
  work as in scan, through `scan.Gate`. Exit 1 when a state read, an account source or a layer
  failed, 4 when every failure was a credential problem, 2 for usage errors.
- With no match, the ownership rules run offline on what the ARN gives (type, account, region,
  name), so packs such as `aws-service` already decide service-linked roles. Without `--index`,
  whois then lists the unmatched types live through the scan layers, one inventory run per
  account over the answers' regions (a global type uses `--region`, else the home region), matches
  the live resource again and reruns the rules with its tags and relations. With `--index` the run
  stays offline unless `--live` is set. `live` in the output says what happened: `not-needed`,
  `offline`, `found`, `not-found` (listed, absent: `undetermined`, rule `whois/not-found`),
  `not-listed` or `no-credentials`.
- `--stdin` reads one query per line, skips blank lines and `#` comments, and drops duplicates.
  `--repo` names the repositories to discover units in, since arguments are queries.
- Formats: tty, plain, md, json (`schemas/whois-v1.json`) and sarif (the scan SARIF builder).

## codify
Turns unmanaged findings into a starting point for adoption, never a final config.
- `tfcensus codify --from scan.json --into live/prod/eu-west-1/network [--types …] [--select <cel>]`
- Writes `imports_tfcensus_<date>.tf` with `import` blocks via hclwrite:
  - `identity = { … }` when the provider type has an identity schema and the target engine supports it (TF ≥ 1.12, OpenTofu ≥ 1.12).
  - Otherwise `id = "…"` (older Terraform and OpenTofu).
  - `to = <type>.<generated_name>`. Names are slugs from tags or name, deterministic and collision-safe.
- Optionally writes `query_tfcensus_<date>.tfquery.hcl` with `list` blocks for types that have provider list resources (278 in aws provider 6.72.1), so `terraform query -generate-config-out=generated.tf` (TF ≥ 1.14) can generate resource config. OpenTofu has no query support yet (opentofu#3787), so the docs say so.
- Never runs plan or apply. Prints the next steps (`terragrunt run -- plan -generate-config-out=$PWD/generated.tf` or `terraform query …`).

Decisions made at M8 (checked against terraform v1.17.2, OpenTofu v1.13.9, terragrunt v1.2.2 and
aws provider v6.72.1 sources):
- Identity schemas come from `internal/typemap/data/generated.yaml`, which `mise run typemap:gen`
  already reads from the provider source (with its `generated.yaml.LICENSE`). The generator now
  also records `list: true` for types with a list resource and `list_required` for the list
  config attributes a block must set. No provider binary, schema command or network is needed.
- Identity when all hold: the engine is terraform or OpenTofu 1.12 or later (OpenTofu added
  import identity in 1.12.0, opentofu#2854), the aws provider in `.terraform.lock.hcl` is 6.0.0
  or later (no lock file means the current provider), and every required identity attribute has a
  value in the scan. Else id, from the curated id template. Else the finding is skipped with the
  reason. `--import-by id` forces ids. When the lock file's provider is older than the typemap's,
  a note says that a type that gained identity later fails with `unknown identity type`.
- The engine comes from the lock file's registry host (`registry.opentofu.org` is OpenTofu), else
  from the `--tf-path` name; the version from `<tf-path> version -json` through execx.
  `--target terraform@1.12.2` skips the detection. A failed detection writes id imports.
- An identity has `account_id` and `region` (the provider rejects a different account, and sets
  the resource's `region` from it) and the type's own attributes, with empty optional ones left
  out. hclwrite sorts the keys.
- Names: the `Name` tag, then the finding's name, then the last segment of its ID or ARN. Lowercase
  ASCII letters, digits and `_`, any other run of characters as one `_`, `r_` before a leading
  digit, at most 64 characters. A name taken in the run, or by a `resource` or `import` block in
  `--into`, gets `_2`, `_3` and so on, in (type, name, ARN or ID) order. A resource that an import
  block in `--into` already imports (same id, or same identity without `account_id` and `region`)
  is skipped.
- Files are `imports_tfcensus_<YYYYMMDD>.tf` and `query_tfcensus_<YYYYMMDD>.tfquery.hcl`, dated by
  the injected clock in UTC. Content has no timestamp, so the same input and target give the same
  bytes. codify writes only directly in `--into` (temp file and rename), refuses to overwrite
  without `--force`, and ignores its own files of the day when it reads existing imports.
- `--query` is opt-in: one list block per type and region (`config { region = … }` for regional
  types) with `include_resource = true`, only for types without required list config. It is
  refused with a note for OpenTofu and terraform before 1.14.
- `--select` is CEL over the ownership `resource` variable, compiled in the rules' environment
  (`in_state` is false). codify takes unmanaged findings only; `--types` filters them as in scan.
- The plan step for a terragrunt unit uses an absolute `-generate-config-out` path, because
  terragrunt runs terraform inside `.terragrunt-cache`.

## Acceptance criteria
- [x] whois golden output for single, double-managed and unmanaged cases (stubbed). Offline mode from a saved index.
- [x] codify output is valid HCL (parsed back with hcl/v2), deterministic (same input → same bytes), and handles names that collide.
- [x] identity vs id choice is covered for TF 1.11, TF 1.12+ and OpenTofu targets.
