# tfcensus ADR-0011: move reads state from S3, pushes through terragrunt and rolls back S3 versions

- Status: accepted (2026-11-12). Amends [ADR-0006](0006-tfmigrate-compatible-move.md).

## Context
ADR-0006 says state I/O goes through `terragrunt run -- state pull|push`. Building M9 showed two
problems with the pull half:
- `state pull` returns the state but not the S3 version it came from. A separate HeadObject
  afterwards can see a newer version written in between, so the recorded VersionId could be wrong.
- `move rollback` must restore states byte for byte (move.md criterion 2). `state push -force`
  re-encodes the state, with the running terraform version in `terraform_version`, so a rollback
  through terraform can't give back the same bytes.

tfmigrate v0.4.6 pushes the destination state before the source state with a plain
`terraform state push` and takes no lock of its own (`tfmigrate/multi_state_migrator.go`).
terraform's S3 backend locks with a `<key>.tflock` object created with `If-None-Match: *`, or a
DynamoDB item, and keeps an MD5 digest of the state in the `<bucket>/<key>-md5` DynamoDB item.

## Decision
- Pull: `s3:GetObject` with the unit's backend credentials (accounts.Backends, the same path as the
  state reader). The body and its VersionId come from one response.
- Push: `terragrunt run --tf-path <tf> -- state push <file>` without `-force`, destinations first,
  then the source. terraform takes the lock, refuses another lineage or an older serial, and
  updates the digest. Right before each push, and for every unit before the first push, a
  HeadObject must still show the VersionId the pull saw.
- Every terragrunt call gets its own `TF_DATA_DIR` and `TG_DOWNLOAD_DIR` in a temp directory, and the
  unit's `.terraform.lock.hcl` is put back afterwards, so a unit's `.terraform`, cache and lock file
  are as they were.
- Rollback: tfcensus takes the backend's own lock (lockfile and/or DynamoDB item, the formats
  terraform writes), copies the recorded pre-push version over the current object with
  `s3:CopyObject`, writes the matching digest, and releases the lock. A unit that had no state is
  deleted again (a delete marker), and its digest item removed. Rollback refuses when a state
  changed after the push, unless `--force`.
- Backups are the S3 versions only. tfcensus writes no local copy of a state except the 0600 temp
  files verify needs, which it deletes. Bucket versioning (S3-VER) is required for apply.
- Confirmation: per block, type the block's last destination unit path (the `from` path for an
  in-place block), or pass `--yes` ([engineering ADR-0007](../../engineering/adr/0007-interactive-prompts.md)).
- Credentials: each unit's S3 calls and terragrunt runs use that unit's own backend credentials and
  configuration, so no call holds two accounts' credentials. Before planning, a moved resource
  whose ARN names another account than the destination's discovered target account is a finding
  (MV-ACCOUNT).
- The migration file reads only `migration "units"` blocks with tfmigrate's action syntax
  (shell-word splitting, `mv`, `xmv` with greedy `*` and `$1`, `rm`). tfmigrate's `state` and
  `multi_state` blocks and `.tfmigrate.hcl` aren't read.

## Consequences
- `move apply` needs `s3:GetObject`, `s3:GetObjectVersion`, `s3:GetBucketVersioning` and the
  write permissions terraform's push needs. Rollback's CopyObject needs `s3:GetObjectVersion` on the
  old version and `s3:PutObject` on the key; it also needs `s3:DeleteObject`, and
  `dynamodb:PutItem` and `dynamodb:DeleteItem` when the backend has a table.
- tfcensus now writes terraform's lock and digest formats itself, for rollback only. A change to
  those formats in terraform needs a matching change here; the moto e2e catches a digest mismatch
  because terraform refuses to read the restored state.
- Units whose `remote_state` doesn't use `generate` can't be verified, because terragrunt passes
  `-backend-config` arguments that the local override backend rejects. tfcensus says so.
