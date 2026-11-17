---
description: How often scan calls a resource unmanaged when something else owns it, measured on three real non-production AWS accounts, with aggregate counts only.
---

# Scan accuracy

A scan is only useful if its `unmanaged` list is short and true. A false positive is a resource
that `scan` calls unmanaged although AWS, another tool or a state owns it. This page records how
many false positives scans of real accounts found, and how each group was fixed.

The numbers are aggregates. The accounts are numbered in the order they were scanned, and no
account ID, name, ARN, tag or resource name appears here.

## How it was measured

1. A read-only `tfcensus scan` ran on a non-production account with a live Terragrunt repository,
   using only the built-in rule packs, and wrote CSV.
2. The maintainer reviewed every `unmanaged` row and marked it as a false positive, a true positive
   or unsure.
3. A script counted the rows by category and verdict, with no identifiers in its output.

The false-positive rate is false positives divided by unmanaged resources. The target is under 5%.

## Results

| Account | Version | Resources | Unmanaged | False positives | FP rate | Undetermined |
|---|---|---|---|---|---|---|
| 1 | v0.3.0 | 1,803 | 171 | 147 | 86% | 650 |
| 1 | v0.4.0 | 1,504 | 38 | 1 | 2.6% | 26 |
| 2 | v0.4.0 | 1,544 | 85 | 1 | 1.2% | 54 |
| 3 | v0.4.0 | 1,453 | 37 | 1 | 2.7% | 26 |

All three accounts are AWS Control Tower member accounts. The v0.4.0 rows come from a build of
v0.4.0 before its release, and the scans ran on 2026-11-16.

The one false positive left in each v0.4.0 row is ElastiCache's system-managed `default.iam-user`,
which the `account-defaults` rule covers since the v0.4.0 release. Account 2 has 4 rows marked
unsure: a Glue database, an SNS topic and an SQS queue that a data platform likely created, and one
network interface with no owner shown.

The true positives are resources made by hand, at account creation, by a Terraform unit whose state
is missing, or by another tool: VPN and Client VPN endpoints, ACM certificates, SSM parameters,
older API Gateway deployments, prefix lists, Route 53 records in a second hosted zone, an MSK
Connect setup, QuickSight and analytics resources, and Resource Explorer itself.

## What the v0.3.0 scan found

The first scan of account 1 had 147 false positives. They fell into these groups, and each fix is
a rule, a [category inheritance](/concepts/categories#inheritance), a native layer or a type mapping:

| Group | Count | Fixed by |
|---|---|---|
| Attachments and inline policies of service-linked and IAM Identity Center roles | 60 | inheritance |
| Children of CloudFormation resources | 8 | inheritance |
| Network interfaces that AWS services created | 27 | `service-eni` |
| Route 53 records that external-dns owns | 8 | `external-dns` |
| Rules of the EKS cluster and Load Balancer Controller security groups | 8 | `eks-cluster-security-group`, `aws-load-balancer-controller-rule`, inheritance |
| AWS defaults | 12 | `apex-ns-soa`, `default-dhcp-options`, `main-route-table`, `regional-nat-route-table`, inheritance |
| Control Tower roles | 4 | `control-tower` |
| QuickSight service roles and policies | 6 | `quicksight-service-role` |
| API Gateway log groups | 5 | `apigateway-log-group` |
| Service-linked secrets | 3 | `service-linked-secret` |
| Flow-log bucket policy | 1 | `log-delivery-bucket-policy` |
| `aws_iam_policy_attachment` and `aws_default_route_table` | 3 | type mappings |
| Deleted Elastic IPs that Resource Explorer still listed | 2 | stale Resource Explorer records dropped |

Reruns of all three accounts found more groups, fixed before the v0.4.0 rows were recorded:

| Group | Count | Fixed by |
|---|---|---|
| The `/` resource of REST APIs a state manages | 3 | derived from the API in state |
| `AutoScalingManagedRule` | 1 | `autoscaling-managed-rule` |
| Pod Identity associations an EKS add-on owns | 2 | `native:eks-pod-identity` |
| The default DHCP options | 1 per account | the config layer reads AWS Config's property names |
| EFS automatic backup plan, vault and selection | 3 per account | `efs-automatic-backup` |
| QuickSight service policies on a managed QuickSight role | 4 in accounts 1 and 3 | `quicksight-policy-attachment` |
| ElastiCache's system-managed `default.iam-user` | 1 per account | `account-defaults` |

Each rule name is on the [rule packs](/reference/rule-packs) page with its expression. A test scans
a stubbed copy of every group and fails if any resource in them comes back `unmanaged`.

## Read your own numbers

Your accounts differ from these. To measure your own rate, scan with CSV output and review the
`unmanaged` rows:

```sh
tfcensus scan --profile acme-nonprod --format csv > scan.csv
```

A resource that is unmanaged and shouldn't be is either a gap in the packs or a resource only your
organization knows about. Write a rule for the second kind ([rules guide](/guides/rules)), and
[open an issue](https://github.com/sassdavid/tfcensus/issues) for the first.
