# Spec: `whois` and `codify` (v0.3)

## whois
`tfcensus whois <arn|id> [--type aws_x]…` uses the StateIndex (F3) across all discovered units:
- Output: unit(s), address, repo path, account and region, and the match key used (identity, arn or id).
- More than one unit means double-managed, shown with a warning and exit 3 under `--fail-on double-managed`.
- No match: also runs the live ownership rules for that single resource (if credentials are available) to explain why it isn't managed (e.g. "other-iac: CloudFormation stack X").
- Batch mode: `--stdin` reads ARNs, one per line (pipe from CloudTrail, Config or a scan report).
- The index can be persisted: `tfcensus inventory --save-index index.json.zst` lets whois run offline, fast, in CI.

## codify
Turns unmanaged findings into a starting point for adoption, never a final config.
- `tfcensus codify --from scan.json --into live/prod/eu-west-1/network [--types …] [--select <cel>]`
- Writes `imports_tfcensus_<date>.tf` with `import` blocks via hclwrite:
  - `identity = { … }` when the provider type has an identity schema and the target engine supports it (TF ≥ 1.12).
  - Otherwise `id = "…"` (OpenTofu, older Terraform).
  - `to = <type>.<generated_name>`. Names are slugs from tags or name, deterministic and collision-safe.
- Optionally writes `query_tfcensus.tfquery.hcl` with `list` blocks for types that have provider list resources (276 in aws provider main), so `terraform query -generate-config-out=generated.tf` (TF ≥ 1.14) can generate resource config. OpenTofu has no query support yet (opentofu#3787), so the docs say so.
- Never runs plan or apply. Prints the next steps (`terragrunt plan -generate-config-out=…` or `terraform query …`).

## Acceptance criteria
- [ ] whois golden output for single, double-managed and unmanaged cases (stubbed). Offline mode from a saved index.
- [ ] codify output is valid HCL (parsed back with hcl/v2), deterministic (same input → same bytes), and handles names that collide.
- [ ] identity vs id choice is covered for TF 1.11, TF 1.12+ and OpenTofu targets.
