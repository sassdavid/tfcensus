| REPO | UNIT | WORKSPACE | STATUS | SERIAL | VERSION | RESOURCES | SIZE | MODIFIED | PROVIDERS |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| tf-fleet | app | default | ok | 7 | 1.12.2 | 7 | 5.0 KiB | 2026-11-09 08:30 | hashicorp/aws 6.20.0, hashicorp/random 3.7.2 |
| tf-fleet | edge | default | ok | 19 | 1.10.6 (opentofu) | 2 | 1.2 KiB | 2026-11-09 08:30 | hashicorp/aws |
| tf-fleet | edge | staging | ok | 3 | 1.10.6 (opentofu) | 1 | 726 B | 2026-11-09 08:30 | hashicorp/aws |
| tf-fleet | legacy | default | unsupported | 88 | 0.11.14 | - | 469 B | 2026-11-09 08:30 | - |
| tf-fleet | network | default | ok | 42 | 1.5.7 (terraform) | 7 | 5.3 KiB | 2026-11-09 08:30 | hashicorp/aws |
| tf-fleet | planned | default | absent | - | - | - | - | - | - |
| tf-fleet | restricted | default | error | - | - | - | - | - | - |
| tf-fleet | secure | default | encrypted | 12 | - | - | 236 B | 2026-11-09 08:30 | - |
| tf-fleet | shared | default | ok | 2 | 1.11.4 | 0 | 181 B | 2026-11-09 08:30 | - |

- tf-fleet/legacy: unsupported state format version 3: tfcensus reads version 4 (Terraform 0.12 and later)
- tf-fleet/restricted: error: access denied: s3:GetObject on s3://acme-restricted-tfstate/restricted/terraform.tfstate; the backend credentials need s3:GetObject, and kms:Decrypt when the bucket uses SSE-KMS
- tf-fleet/restricted: workspaces not listed: access denied: s3:ListBucket on s3://acme-restricted-tfstate/env:/; the backend credentials need s3:ListBucket
- tf-fleet/secure: state is encrypted with OpenTofu client-side encryption, so only serial and lineage are readable; tofu show -json in the unit reads it with the unit's key configuration
- double-managed aws_security_group arn:aws:ec2:eu-west-1:111111111111:security-group/sg-0123456789abcdef0 by tf-fleet/app (aws_security_group.legacy_web) and tf-fleet/network (aws_security_group.web) (matched on arn)

9 states: 5 ok, 1 absent, 1 encrypted, 1 unsupported, 1 error, 0 skipped; 17 resource instances, 1 double-managed
