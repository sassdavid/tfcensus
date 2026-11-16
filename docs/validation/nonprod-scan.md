# Scan accuracy on non-prod accounts

Read-only `tfcensus scan` runs on real non-prod accounts, one row per account and version. This
record holds aggregates only: no account IDs, names, ARNs or tag values. Accounts are numbered in
the order they were scanned. The spec and the false-positive groups are in
[scan-accuracy.md](../plan/specs/scan-accuracy.md), and the decisions in
[ADR-0017](../plan/adr/0017-scan-accuracy.md).

## How a row is made

1. Run `tfcensus scan <live repo> --profile <profile> --format csv > scan.csv` with a read-only
   profile. Keep `scan.csv` outside this repository.
2. Add a `verdict` column (`fp`, `tp` or `unsure`) to every `unmanaged` row, and optionally a
   `group` column that names the false-positive group.
3. Run `mise run validation:stats -- scan.csv`. It prints the counts by category, verdict, type
   and group, with no ID, name or ARN. Copy its first table into the row below.

The false-positive rate is `fp / unmanaged`. The target is under 5% (ownership-rules.md
criterion 5).

## Rows

| Account | Date | Version | Resources | Unmanaged | False positives | FP rate | Undetermined | False negatives found |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 2026-11-16 | v0.3.0 | 1,803 | 171 | 147 | 86% | 650 | VPC Lattice resource gateways and resource configurations, ACM certificates, API Gateway domain names, EKS add-ons and node groups, VPC endpoint services, Route 53 Resolver rules, Backup recovery points |
| 1 | pending | v0.4.0 | | | | | | |
| 2 | pending | v0.4.0 | | | | | | |
| 3 | pending | v0.4.0 | | | | | | |

Account 1 is one Control Tower member account, two regions, built-in packs only. Its row for
v0.4.0 comes from the owner's read-only rerun with the same command (M21's last box).

## False positives by group, account 1, v0.3.0

| Group | Count | Fixed by | Fixture |
| --- | --- | --- | --- |
| Attachments and inline policies of service-linked and `AWSReservedSSO_*` roles | 60 | inheritance | `testdata/validation/groups/01-service-role-children.json` |
| Children of CloudFormation resources | 8 | inheritance | `02-cloudformation-children.json` |
| ENIs that AWS services created with `RequesterManaged=false` | 27 | `service-eni` | `03-service-enis.json` |
| Route 53 records that external-dns owns | 8 | `external-dns` | `04-external-dns.json` |
| Rules of the EKS cluster and Load Balancer Controller security groups | 8 | `eks-cluster-security-group`, `aws-load-balancer-controller-rule`, inheritance | `05-eks-and-lbc-rules.json` |
| AWS defaults | 12 | `apex-ns-soa`, `default-dhcp-options`, `main-route-table`, `regional-nat-route-table`, inheritance | `06-aws-defaults.json` |
| `AWSControlTowerExecution` and `AWSControlTower_VPCFlowLogsRole` | 4 | `control-tower` | `07-control-tower.json` |
| QuickSight service roles and policies | 6 | `quicksight-service-role` | `08-quicksight.json` |
| API Gateway log groups | 5 | `apigateway-log-group` | `09-apigateway-log-groups.json` |
| Service-linked secrets | 3 | `service-linked-secret` | `10-service-linked-secrets.json` |
| Flow-log bucket policy | 1 | `log-delivery-bucket-policy` | `11-log-delivery-policy.json` |
| `aws_iam_policy_attachment` and `aws_default_route_table` | 3 | curated types | `12-policy-attachment-and-default-route-table.json` |
| Deleted Elastic IPs still in Resource Explorer | 2 | stale Resource Explorer records dropped | `13-stale-resource-explorer.json` |

## False positives found by the v0.4.0 rerun, account 1

The first rerun (52 `unmanaged`) found three more groups. They are fixed before the v0.4.0 row
is recorded, and the owner reruns the scan for it.

| Group | Count | Fixed by | Fixture |
| --- | --- | --- | --- |
| The `/` resource of REST APIs a state manages | 3 | `derived.state_attr` (`root_resource_id`) | `14-rerun.json` |
| `AutoScalingManagedRule` | 1 | `autoscaling-managed-rule` | `14-rerun.json` |
| Pod Identity associations an EKS add-on owns | 2 | `native:eks-pod-identity` reads `ownerArn` | `14-rerun.json` |

## False positives found by accounts 2 and 3

Accounts 2 and 3 are Control Tower member accounts in `ap-southeast-1`, built-in packs only. Their
first scans (89 and 45 `unmanaged`) found three groups that account 1 has too. They are fixed
before the rows are recorded, and the owner reruns all three accounts.

| Group | Count (accounts 1, 2, 3) | Fixed by | Fixture |
| --- | --- | --- | --- |
| The default DHCP options | 1, 1, 1 | the config layer reads AWS Config's CloudFormation property names | `15-further-accounts.json` |
| EFS automatic backup plan, vault and selection | 3, 3, 3 | `efs-automatic-backup` | `15-further-accounts.json` |
| QuickSight service policies on a managed QuickSight role | 4, 0, 4 | `quicksight-policy-attachment` | `15-further-accounts.json` |
| ElastiCache's system-managed `default.iam-user` (found in the review of the rerun) | 1, 1, 1 | `account-defaults` | `15-further-accounts.json` |

`TestSpec_scan_accuracy_9` scans all 15 fixtures with stubbed AWS
(`testdata/cassettes/aws/validation.yaml`) and finds no `unmanaged` resource.
