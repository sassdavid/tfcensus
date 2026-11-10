# Spec: state reader and StateIndex (F3)

## Reading
- `s3:GetObject` on `<bucket>/<key>` (workspace prefix `env:/<ws>/` when applicable), using backend credentials. **No locks are taken**: read-only and safe during applies.
- Parse **raw state v4 JSON** into our own minimal struct. terraform-json models `show -json`, not the raw file. Fields: `version`, `terraform_version`, `serial`, `lineage`, `resources[]{mode,type,name,module,provider,instances[]{index_key,attributes,identity,identity_schema_version,schema_version}}`.
- Data sources (`mode: data`) are excluded from the index but counted in the inventory.
- Provider address normalization: `registry.opentofu.org/hashicorp/aws` ≡ `registry.terraform.io/hashicorp/aws`.
- `terraform_version` can't tell Terraform from OpenTofu. Report the "engine" only when known from the unit (`--tf-path`, `.opentofu-version`, a tofu-only marker).
- Streaming decode (`json.Decoder`) handles very large states without loading the attributes we don't need: decode lazily, keep only identifiers.

## Edge cases
| Case | Behaviour |
|---|---|
| **OpenTofu client-side encryption** (1.7+; payload has `encrypted_data`/`encryption_version`) | report `encrypted: true` and skip with a hint. Optional `--decrypt-with-tofu` runs `tofu show -json` in the unit (needs its key config). The code is in OpenTofu `internal/`, so it can't be imported |
| Empty state (0 resources) | indexed, flagged by audit |
| State v3 or older | unsupported: warn with the version |
| Key missing (unit never applied) | `state: absent` |
| Access denied | per-unit error, the run continues |

## StateIndex
Maps each managed resource instance to keys used by matching ([matching.md](matching.md)):
- `identity` (normalized map → canonical string, per type)
- `arn` attribute if present
- `id` attribute + type → normalized ID via `typemap`
- derived account and region (from ARN, provider config, or unit)

Collisions (the same key from two units) feed **double-managed** detection.

## Inventory output (`tfcensus inventory`)
Per unit: state serial, lineage, terraform_version, providers, resource count by type, size, LastModified, encrypted?, errors.
Raw state records provider addresses but not their versions (checked in terraform's `internal/states/statefile/version4.go`). Provider versions come from the unit's `.terraform.lock.hcl` when the repo has one, marked `version_source: lockfile`, and are absent otherwise.
`--format json` (schema `schemas/inventory-v1.json`) is also an input for relgap (terraform versions from state and provider versions from lock files across the fleet; see [integration.md](../integration.md)).
`--save-index <file>` writes the StateIndex (schema `schemas/index-v1.json`): zstd for `.zst`, gzip for `.gz`, plain JSON otherwise.

## Acceptance criteria
- [x] Fixture states: TF 1.5 (no identity), TF 1.12+ (identity), OpenTofu encrypted, workspace prefix, 50 MB synthetic state. Golden index output; the 50 MB state is indexed with under 200 MB RSS (benchmark).
- [x] Attribute values never appear in any output except identifiers (test greps rendered output for fixture secrets).
- [x] Two fixture units managing the same SG → double-managed in the index.
- [x] 500 states over a stubbed S3 at 16 concurrency complete in under 5 s.
