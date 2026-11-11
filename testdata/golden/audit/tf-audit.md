| SEVERITY | CHECK | TARGET | REASON |
| --- | --- | --- | --- |
| high | S3-VER | s3://acme-audit-bad | versioning is suspended: an overwritten or deleted state can't be restored |
| high | S3-KMSPOL | s3://acme-audit-legacy | key arn:aws:kms:eu-west-1:111111111111:key/0b1c2d3e-open-4000-8000-000000000003 allows kms:Decrypt to principals in any account (statement AllowDecryptAnywhere) |
| high | S3-PAB | s3://acme-audit-bad | bucket: BlockPublicAcls, IgnorePublicAcls, BlockPublicPolicy, RestrictPublicBuckets off |
| high | S3-TLS | s3://acme-audit-bad | the bucket has no policy, so plain HTTP requests aren't denied |
| high | S3-TLS | s3://acme-audit-legacy | plain HTTP isn't denied for s3:GetObject by anonymous, another account, the bucket's account, arn:aws:iam::111111111111:root |
| high | LK-STALE | dynamodb://eu-west-1/acme-audit-locks-bad acme-audit-bad/legacy/terraform.tfstate | lock held for 3d3h, over 24h |
| high | LK-STALE | s3://acme-audit-bad/legacy/terraform.tfstate.tflock | lock file is 3d0h old, over 24h |
| high | FL-LINEAGE | tf-audit/app | lineage 1a2b3c4d-0000-4000-8000-000000000002 is shared by tf-audit/app (s3://acme-audit-good/app/terraform.tfstate) and tf-audit/copy (s3://acme-audit-good/copy/terraform.tfstate) |
| high | FL-LINEAGE | tf-audit/copy | lineage 1a2b3c4d-0000-4000-8000-000000000002 is shared by tf-audit/app (s3://acme-audit-good/app/terraform.tfstate) and tf-audit/copy (s3://acme-audit-good/copy/terraform.tfstate) |
| high | FL-DOUBLE | tf-audit/app | aws_iam_role arn:aws:iam::111111111111:role/acme-audit-app is managed by tf-audit/app (aws_iam_role.app) and tf-audit/copy (aws_iam_role.app) (matched on arn) |
| medium | S3-ENC | s3://acme-audit-bad | default encryption is SSE-S3: anyone with s3:GetObject can read state, no key policy applies |
| medium | S3-OWN | s3://acme-audit-bad | object ownership is ObjectWriter: ACLs are on |
| medium | S3-REPL | s3://acme-audit-bad | no replication: losing the region loses every state |
| medium | S3-REPL | s3://acme-audit-legacy | replica acme-audit-legacy-replica is in the same region; replica acme-audit-legacy-replica versioning is suspended; replica acme-audit-legacy-replica: default encryption is SSE-S3: anyone with s3:GetObject can read state, no key policy applies |
| medium | S3-LC | s3://acme-audit-bad | no lifecycle rule expires noncurrent versions of every state: they are kept forever |
| medium | S3-LC | s3://acme-audit-legacy | noncurrent versions expire after 3 days, under 7: a bad apply found late can't be rolled back |
| medium | LK-PITR | dynamodb://eu-west-1/acme-audit-locks-bad | encrypted with an AWS owned key, not KMS; point-in-time recovery is off |
| medium | FL-ORPHAN | s3://acme-audit-bad/retired/terraform.tfstate | no discovered unit resolves to this key |
| medium | FL-ORPHAN | s3://acme-tfstate-orphans/lost/terraform.tfstate | state in a discovered bucket that no unit uses |
| medium | FL-TFVER | tf-audit/legacy | written by 1.5.7, older than the policy's 1.10 |
| low | S3-LOG | s3://acme-audit-bad | no server access logging and no CloudTrail trail records S3 data events for the bucket |
| low | S3-LEGACY | s3://acme-audit-legacy | the policy has the RootAccess statement that terragrunt before 1.2 added |
| low | LK-MODE | tf-audit/legacy legacy/terraform.tfstate | locks with dynamodb_table acme-audit-locks-bad only |
| low | LK-MD5 | dynamodb://eu-west-1/acme-audit-locks-bad acme-audit-bad/gone/terraform.tfstate-md5 | digest for s3://acme-audit-bad/gone/terraform.tfstate, which doesn't exist |
| low | FL-ABSENT | tf-audit/planned | s3://acme-audit-good/planned/terraform.tfstate doesn't exist: never applied, or a wrong key |
| low | FL-EMPTY | tf-audit/empty | the state manages no resources |
| low | FL-PROV | tf-audit/legacy | hashicorp/aws 4.67.0 in .terraform.lock.hcl is older than the policy's 5.0 |
| low | FL-SIZE | tf-audit/legacy | state is 4.8 KiB, over the policy's 0.004 MiB |
| info | LK-BOTH | tf-audit/app app/terraform.tfstate | locks with both use_lockfile and dynamodb_table acme-audit-locks-good |
| info | FL-SECRET | tf-audit/secrets | sensitive-looking attributes stored in plain state: aws_db_instance.main (password); aws_iam_access_key.ci (secret, ses_smtp_password_v4); random_password.db (bcrypt_hash, result) |

| BUCKET | ACCOUNT | REGION | SOURCE | UNITS | STATES | VERSIONING | ENCRYPTION | REPLICATION |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| acme-audit-bad | 111111111111 | eu-west-1 | unit | 1 | 2 | Suspended | AES256 | none |
| acme-audit-good | 111111111111 | eu-west-1 | unit | 5 | 4 | Enabled | aws:kms (customer-managed key) | acme-tfstate-good-replica (eu-central-1, enabled) |
| acme-audit-legacy | 111111111111 | eu-west-1 | unit | 1 | 1 | Enabled | aws:kms (customer-managed key) | acme-audit-legacy-replica (eu-west-1, enabled) |
| acme-tfstate-good-replica | 111111111111 | eu-central-1 | discover | 0 | 4 | Enabled | aws:kms (customer-managed key) | replica of acme-tfstate-orphans |
| acme-tfstate-orphans | 111111111111 | eu-west-1 | discover | 0 | 1 | Enabled | aws:kms (customer-managed key) | acme-tfstate-good-replica (eu-central-1, enabled) |

| LOCK TABLE | ACCOUNT | REGION | UNITS | PITR | ENCRYPTION | LOCKS |
| --- | --- | --- | --- | --- | --- | --- |
| acme-audit-locks-bad | 111111111111 | eu-west-1 | 1 | off | AWS owned key | 1 |
| acme-audit-locks-good | 111111111111 | eu-west-1 | 1 | on | KMS | 1 |

- suppressed S3-MFA s3://acme-audit-bad (MFA delete needs the root user's device, until 2027-06-30)
- suppressed S3-MFA s3://acme-audit-legacy (MFA delete needs the root user's device, until 2027-06-30)
- warning: audit.suppress[1] (check S3-LOG) expired on 2026-10-31 and no longer applies: 1 finding(s) reported again

30 findings (10 high, 10 medium, 8 low, 2 info), 2 suppressed; 5 buckets, 2 lock tables; 0 checks could not run
