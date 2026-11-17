# Spec: accounts, credentials and the capability probe (F2)

## Account sources
- **Profiles:** `--profile` globs over `~/.aws/config` (including `sso-session`). One `aws.Config` per profile, loaded once, so SSO prompts once at most. `sts:GetCallerIdentity` maps profile → account.
- **Organizations:** `--org` with a management or delegated-admin profile lists accounts (`ListAccounts`, ACTIVE only), then assumes `--org-role` (default `OrganizationAccountAccessRole`, configurable per OU via config).
- **Backend credentials:** state reads use the unit's backend `assume_role.role_arn` (TF 1.6+ form) or Terragrunt `remote_state` role settings, chained from the base profile that owns the bucket's account. Separate from scan credentials.
- **Config mapping file** (optional): `accounts: { "111111111111": { profile: acme-nonprod, name: acme-nonprod } }`, for when profile names don't match accounts.

## Errors
- Expired SSO token: detected once per profile. The message says `aws sso login --profile X` (or `--sso-session Y`). The profile is skipped and listed in report `errors`, with exit 4 if every profile failed.
- AccessDenied for one API: record a per-layer capability "denied" and continue with the other layers.

## Capability probe (`tfcensus doctor`, and implicitly before `scan`)
For each (account, region), cached 24 h:

| Check | API | Used for |
|---|---|---|
| Resource Explorer index type | `resource-explorer-2:ListIndexes` | LOCAL vs AGGREGATOR; region coverage |
| Resource Explorer view | `ListViews` / `GetDefaultView` | `ListResources` needs a view. Without one the RE layer is unavailable (we never create views; read-only) |
| Config recorder | `DescribeConfigurationRecorders` + `...Status` | recording? allSupported? CONTINUOUS/DAILY? |
| Config aggregator | `DescribeConfigurationAggregators` + `DescribeConfigurationAggregatorSourcesStatus` (in the configured audit/aggregator account) | org-wide `SelectAggregateResourceConfig` for the pairs whose sources succeeded ([inventory](inventory.md#config-aggregator-fast-path)) |
| Cloud Control | always available, throttled | fallback layer |

**Real-world check (2026-11-09, a non-prod Control Tower member account; `acme-nonprod` in examples):**
- Resource Explorer: only **LOCAL** indexes in 4 regions, no aggregator. `ListSupportedResourceTypes` = **649** types.
- Config: the Control Tower baseline recorder is **recording CONTINUOUS, allSupported**, in eu-west-1 and eu-central-1, with none in us-east-1. No aggregator in the member account (Control Tower keeps it in the Audit account).

This is why the layer order is decided per account/region by the probe, not hard-coded ([adr/0003](../adr/0003-capability-probed-layered-inventory.md)).

## Read-only IAM policy (shipped as `docs/iam/tfcensus-readonly.json`)
- `sts:GetCallerIdentity`, `organizations:List*`
- `account:ListRegions` (`--all-regions`: the regions each account has enabled)
- `s3:ListBucket`, `s3:GetObject` (state buckets), `s3:GetBucket*`, `s3:GetEncryptionConfiguration`, `s3:GetLifecycleConfiguration`, `s3:GetReplicationConfiguration`, `s3:GetBucketPolicy`, `s3:GetBucketPublicAccessBlock`, `s3:GetBucketOwnershipControls`, `s3:GetBucketLogging`, `s3:GetBucketVersioning`, `s3:GetAccountPublicAccessBlock`
- `dynamodb:DescribeTable`, `dynamodb:DescribeContinuousBackups`, `dynamodb:Scan` (lock tables only, resource-scoped)
- `kms:DescribeKey`, `kms:GetKeyPolicy`, and `kms:Decrypt` through S3 only (`kms:ViaService`)
- `resource-explorer-2:List*`/`Get*`; `config:Describe*`/`Select*`/`Get*`
- `cloudformation:ListResources`/`GetResource` (Cloud Control), and `vpc-lattice:List*` plus `vpc-lattice:GetResourceConfiguration` for the handlers of the VPC Lattice types scan lists through it by default ([ADR-0017](../adr/0017-scan-accuracy.md))
- `cloudformation:ListStacks`, `cloudformation:ListStackResources` (the CloudFormation source of scan, config `scan.cloudformation`)
- the native enumerators: `ec2:Describe*`, `iam:GetAccountAuthorizationDetails`, `route53:List*`,
  `eks:ListPodIdentityAssociations`, and the S3 bucket reads above
- Nothing else: `TestSpec_accounts_4` also fails on a granted action that no cassette calls,
  unless AWS calls it on the caller's behalf (`kms:Decrypt`, the VPC Lattice handler actions)
- Equivalent to AWS-managed `ReadOnlyAccess` + `ViewOnlyAccess` combined. Docs say so.

## Acceptance criteria
- [x] An expired-SSO fixture (stubbed credential provider error) produces one clear message per profile, the run continues, and exit 4 only when all fail.
- [x] `doctor` golden output from stubbed responses, mirroring the real-world check facts above.
- [x] Org mode: 3 stubbed accounts, one AccessDenied on assume-role → reported, the others scanned.
- [x] Every AWS action the code can call (extracted from recorded HTTP traffic in tests) is covered by the IAM policy JSON (a static test), and every granted action has a caller or a written reason (the reverse check). Optional, manual: `aws accessanalyzer validate-policy` and a read-only real-account run.
