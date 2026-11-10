# Spec: `tfcensus audit backend`

Backends are discovered from units (F1) and, with `--discover`, from bucket scans. Each check has an ID,
a severity, a remediation hint, and optional suppression in config (`audit.suppress: [{check, bucket, reason, expires}]`).

## Bucket checks

| ID | Check | Severity |
|---|---|---|
| S3-VER | versioning enabled | high |
| S3-MFA | MFA delete (info only; rarely practical) | low |
| S3-ENC | default SSE-KMS with a customer-managed key (SSE-S3 → medium) | medium |
| S3-KMSPOL | KMS key policy doesn't grant `kms:Decrypt` to `*` principals | high |
| S3-PAB | bucket + account public access block all true | high |
| S3-TLS | bucket policy denies `aws:SecureTransport=false`. Evaluated properly; Prowler's version has a false pass (#13226) | high |
| S3-OWN | ObjectOwnership BucketOwnerEnforced | medium |
| S3-REPL | cross-region replication configured (and the replica bucket passes S3-VER/ENC) | medium |
| S3-LC | lifecycle for noncurrent versions exists, keeping more than N days (default 30). Flags missing (unbounded cost) and too-aggressive (under 7 d) | medium |
| S3-LOG | access logging or CloudTrail data events | low |
| S3-LEGACY | legacy `RootAccess` statement (terragrunt < 1.2 wrote it) | low |

## Lock checks

| ID | Check | Severity |
|---|---|---|
| LK-MODE | unit uses `dynamodb_table` only (deprecated since TF 1.11) → suggest `use_lockfile` | low |
| LK-BOTH | both lockfile and DynamoDB configured: fine for migration, flagged as info | info |
| LK-PITR | DynamoDB lock table PITR + KMS | medium |
| LK-STALE | `.tflock` objects or DynamoDB `LockID` items older than 24 h (configurable) | high |
| LK-MD5 | orphaned `-md5` digest items with no matching state key | low |

## Fleet checks (only possible with discovery + state index)

| ID | Check | Severity |
|---|---|---|
| FL-ORPHAN | state key that no unit resolves to | medium |
| FL-ABSENT | unit whose state key doesn't exist (never applied, or wrong key) | low |
| FL-EMPTY | state with 0 managed resources | low |
| FL-LINEAGE | same `lineage` under two keys (copied state) | high |
| FL-DOUBLE | same resource identity/ARN in more than one state | high |
| FL-TFVER | `terraform_version` older than the fleet policy (e.g. < 1.10) | medium |
| FL-PROV | provider version (from `.terraform.lock.hcl`; raw state has none) older than the policy | low |
| FL-SIZE | state over 20 MB (slow plans) | low |
| FL-SECRET | sensitive-looking attribute keys stored in plain state (names only, never values) | info |

## Acceptance criteria
- [ ] Each check has stubbed positive and negative fixtures and a golden finding.
- [ ] S3-TLS uses real policy evaluation (handles `Bool` vs `BoolIfExists`, NotPrincipal, multiple statements), with tests for the Prowler #13226 shape.
- [ ] Suppressions with `expires` behave like ownership rules (warning after expiry).
- [ ] The live validation on the author's fleet reproduces known replication status per state bucket (recorded in `docs/validation/`).
