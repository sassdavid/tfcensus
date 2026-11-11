# Backend audit: live validation

Read-only run of `tfcensus audit backend` on 2026-11-11 against one AWS account through the
`collection-x-product` profile. This record holds aggregates only: no bucket names, account IDs
or keys. Buckets are numbered by name order.

## Command

```sh
cd "$(mktemp -d)"   # no repository: every bucket comes from --discover
tfcensus audit backend . --profile collection-x-product --discover --format json > audit.json
```

The account has 76 buckets. Two match the default `--discover-pattern` globs
(`*tfstate*`, `*terraform-state*`, `*tf-state*`). The run exits 0, with no errors and no check
that couldn't run.

## Replication status per state bucket

The known status comes from a separate read-only script (AWS MCP `run_script`) that calls
`ListBuckets`, `GetBucketLocation`, `GetBucketVersioning` and `GetBucketReplication` directly.

| Bucket | Known: rules (enabled), cross-region | Audit: rules (enabled), cross-region | Audit: replica of another audited bucket | S3-REPL finding |
| --- | --- | --- | --- | --- |
| bucket-1 | 1 (1), yes | 1 (1), yes | no | no |
| bucket-2 | 0 (0), no | 0 (0), no | yes (bucket-1) | no |

Both buckets have versioning enabled in both reads. The audit reproduces the known replication
status of every state bucket.

The first run reported S3-REPL on bucket-2 and counted the state copies in it as orphans. A
replica needs no replication of its own, so the audit now marks a bucket that another audited
bucket replicates to (`replica_of`) and treats its keys as the source's. The run above is the
second one.

## Findings

| Check | Findings |
| --- | --- |
| FL-ORPHAN | 378 (188 in bucket-1, 190 in bucket-2) |
| S3-LC | 2 |
| S3-LOG | 2 |
| S3-MFA | 2 |
| S3-PAB | 2 |

FL-ORPHAN counts every state key, because the run had no repository and so no unit resolves to
any key. A run over the repositories that own these states would show only the real orphans.
No lock tables were audited: lock tables come from unit backends.
