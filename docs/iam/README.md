# Read-only IAM policy

[`tfcensus-readonly.json`](tfcensus-readonly.json) grants everything tfcensus calls, except
`move apply|rollback|mark-applied` (the only commands that write, and only to state; [ADR-0007](../plan/adr/0007-read-only-by-default.md)).
It is the action list from the [accounts spec](../plan/specs/accounts-and-credentials.md#read-only-iam-policy).
Together it is roughly the AWS-managed `ReadOnlyAccess` and `ViewOnlyAccess` combined, so a role with those two managed
policies (plus `kms:Decrypt` for SSE-KMS state) works as well.

Narrow it before use:
- `ReadStateObjects`: replace `arn:aws:s3:::*/*` with your state buckets (`arn:aws:s3:::my-tfstate-*/*`). State files
  contain secrets. tfcensus prints only identifiers, but the role can read everything in those objects.
- `DecryptStateViaS3Only`: limit `Resource` to the KMS keys that encrypt state. The `kms:ViaService` condition already
  restricts decryption to requests made through S3.
- `LockTables`: limit to your lock tables (`arn:aws:dynamodb:*:*:table/terraform-locks*`). `dynamodb:Scan` is used only
  to look for stale locks.

Org mode (`--org`) also needs `sts:AssumeRole` on the member-account role (default `OrganizationAccountAccessRole`).
Grant it on the scanning principal for exactly that role name. It is not part of this policy, because what the assumed
role may do is defined in each member account.

Optional validation (manual, needs an AWS account):

```sh
aws accessanalyzer validate-policy --policy-type IDENTITY_POLICY \
  --policy-document file://docs/iam/tfcensus-readonly.json
```
