---
description: The read-only IAM policy for every tfcensus command except move apply, rollback and mark-applied, what each statement is for, and the permissions of the move commands.
---

# IAM policies

## Read-only policy

`tfcensus-readonly.json` grants every AWS action tfcensus calls, except those of
`move apply`, `move rollback` and `move mark-applied`. Those three are the only commands that write,
and only to state ([safety model](/concepts/safety)). Download it from
[`/iam/tfcensus-readonly.json`](/iam/tfcensus-readonly.json):

```sh
curl -fsSLO https://tfcensus.sasskovacs.dev/iam/tfcensus-readonly.json
```

<<< @/iam/tfcensus-readonly.json

The policy is a subset of the AWS-managed `ReadOnlyAccess` and `ViewOnlyAccess` policies combined.
A role with those two managed policies works too, plus `kms:Decrypt` when state uses SSE-KMS.

### What each statement is for

| Statement | Used by | Notes |
|---|---|---|
| `Identity` | every command that calls AWS | `sts:GetCallerIdentity` names the account of each profile. |
| `Organizations` | `--org` | Lists the accounts and their OUs from the management account. |
| `EnabledRegions` | `--all-regions` | `account:ListRegions` lists the regions each account has enabled. Drop it if you never set `all_regions`. |
| `StateBucketsAndAudit` | `inventory`, `audit backend`, `scan` | Bucket settings for the audit checks and the S3 layer, and `s3:ListBucket` to find workspaces. |
| `ReadStateObjects` | every command that reads state | State files hold secrets. tfcensus prints only identifiers, but the role can read everything in the objects. |
| `DecryptStateViaS3Only` | reading SSE-KMS state | S3 calls KMS with the caller's permissions. The `kms:ViaService` condition limits decryption to requests made through S3. |
| `KeyAudit` | `audit backend` | Reads the state key's description and policy (S3-ENC, S3-KMSPOL). |
| `StateAccessLogging` | `audit backend` | Reads trail event selectors to see whether CloudTrail logs S3 data events for a bucket without server access logging (S3-LOG). |
| `LockTables` | `audit backend` | `dynamodb:Scan` looks for stale locks and orphaned digests. Lock items hold lock metadata, not state. |
| `ResourceExplorer` | `doctor`, `scan`, `whois --live` | The index, the default view and its resources. |
| `Config` | `doctor`, `scan`, `whois --live` | Recorder status, aggregators and `Select*` queries over recorded resources. |
| `CloudControlRead` | `scan` | The Cloud Control layer. |
| `CloudControlHandlers` | `scan` | Cloud Control runs each type's list and read handlers with the caller's permissions. By default `scan` lists the VPC Lattice resource gateways, resource configurations and service network associations this way, because neither AWS Config nor Resource Explorer lists them. Types you add to `scan.cloudcontrol_types` need their own handler permissions (the `handlers` of the type's schema in the CloudFormation registry). |
| `CloudFormationStacks` | `scan` | Lists stack resources to find what CloudFormation owns. It reads stack and resource identifiers, never templates or outputs. Drop it if you set `scan.cloudformation: false`. |
| `NativeEnumerators` | `scan`, `whois --live` | The native layers: security group rules, NAT gateways, IAM attachments, Route 53 records and EKS Pod Identity associations ([inventory layers](/reference/type-coverage#inventory-layers)). |

The policy grants nothing that tfcensus doesn't call. A test takes every AWS action from the
recorded API traffic of the test suite and fails when the policy is missing one. It also fails
when the policy grants an action that no recorded call needs. Three actions are exempt from that
second check, because AWS calls them on the caller's behalf: `kms:Decrypt` (S3 reads an SSE-KMS
object) and the two `vpc-lattice` actions (Cloud Control's handlers for the VPC Lattice types).

### Narrow it

- `ReadStateObjects`: replace `arn:aws:s3:::*/*` with your state buckets, such as
  `arn:aws:s3:::acme-tfstate-*/*`.
- `DecryptStateViaS3Only`: limit `Resource` to the KMS keys that encrypt state.
- `LockTables`: limit `Resource` to your lock tables, such as
  `arn:aws:dynamodb:*:*:table/terraform-locks*`.
- Drop the statements of features you don't use: `EnabledRegions`, `Organizations` without
  `--org`, `CloudFormationStacks` with `scan.cloudformation: false`.

### Assumed roles

`--org` also needs `sts:AssumeRole` on the member-account role (default
`OrganizationAccountAccessRole`, or the roles in `accounts.org.ou_roles`). So does a backend whose
`remote_state` names a `role_arn`. Grant it on the scanning principal for exactly those roles. It
isn't part of the policy, because what an assumed role may do is defined in its own account, and
that role needs this policy.

### Check it

AWS IAM Access Analyzer checks the policy's grammar and flags overly broad statements:

```sh
aws accessanalyzer validate-policy --policy-type IDENTITY_POLICY \
  --policy-document file://tfcensus-readonly.json
```

## Move permissions

The move commands read and write the state of every involved unit with that unit's own backend
credentials. They need, for every involved backend:

| Command | Permissions |
|---|---|
| `move suggest` | what `terragrunt plan` needs in each unit |
| `move plan`, `move status` | `s3:GetObject`, `s3:GetBucketVersioning`, plus what `terragrunt plan` needs in the unit |
| `move apply` | the above, `s3:GetObjectVersion`, and what `terraform state push` needs: `s3:PutObject` on the state key, plus the lock (`s3:PutObject` and `s3:DeleteObject` on `<key>.tflock`, or `dynamodb:PutItem`, `dynamodb:GetItem` and `dynamodb:DeleteItem`) and `s3:PutObject` on `<key>.tfcensus-migrations.json` |
| `move rollback` | the `move apply` set, plus `s3:DeleteObject` on the state key |
| `move mark-applied` | `s3:GetObject` and `s3:PutObject` on `<key>.tfcensus-migrations.json` |
