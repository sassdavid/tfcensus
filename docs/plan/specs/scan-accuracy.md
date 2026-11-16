# Spec: scan accuracy on real accounts

The first read-only scan of a real non-prod account (2026-11-16, tfcensus v0.3.0, one Control
Tower member account, two regions, built-in packs only) found most `unmanaged` findings false and
most `undetermined` findings avoidable. This spec lists what scan gets wrong, the decisions that
fix it, and how accuracy is measured on that account and on the accounts that follow.
Decisions: [ADR-0017](../adr/0017-scan-accuracy.md). Aggregates only, as everywhere in
`docs/validation/` (open question Q7): no account IDs, names or ARNs in this repo.

## What the first run found

| Category | Count |
|---|---|
| managed | 770 |
| undetermined | 650 |
| unmanaged | 171 |
| derived | 93 |
| aws-managed | 75 |
| other-iac | 44 |

### False `unmanaged` findings (147 of 171, 86%)

| Group | Count | Cause | Section |
|---|---|---|---|
| Policy attachments and inline policies of service-linked and `AWSReservedSSO_*` roles | 60 | The role is `aws-managed`, its children don't inherit that | [Inheritance](#inheritance) |
| Children of CloudFormation resources: attachments and inline policy of `aws-controltower-*` roles, the subresources of a state bucket and the inline policy of its replication role (both from a stack) | 8 | Same | [Inheritance](#inheritance) |
| ENIs that MSK, Lambda, QuickSight, Client VPN and Managed Service for Apache Flink created, all with `RequesterManaged=false` | 27 | `service-eni` reads two flags only | [ENIs](#enis-created-by-aws-services) |
| Route 53 records that external-dns owns (4 TXT, 4 A ALIAS) | 8 | No rule | [Kubernetes controllers](#kubernetes-controllers) |
| Security group rules of the EKS cluster security group and of AWS Load Balancer Controller groups, and its rules on node groups | 8 | No rule, and no parent inheritance | [Kubernetes controllers](#kubernetes-controllers) |
| AWS defaults: apex NS and SOA (4), default DHCP options (2), rules of default security groups (4), the main route table of a VPC from a stack (1), the route table of a regional NAT gateway (1) | 12 | Missing or too narrow `aws-defaults` rules | [AWS defaults](#aws-defaults) |
| `AWSControlTowerExecution` and `AWSControlTower_VPCFlowLogsRole` with their attachments | 4 | `control-tower` matches only `aws-controltower-` | [AWS services](#aws-services) |
| QuickSight service roles and policies under `/service-role/` | 6 | No rule | [AWS services](#aws-services) |
| API Gateway execution log groups and `/aws/apigateway/welcome` | 5 | No rule | [AWS services](#aws-services) |
| Service-linked secrets (`rds!db-...`, `s2svpn!...`) | 3 | No rule | [AWS services](#aws-services) |
| A flow-log bucket policy that log delivery added | 1 | No rule | [AWS services](#aws-services) |
| An `aws_iam_policy_attachment` (2 live attachments) and an `aws_default_route_table` | 3 | Not curated, so never matched | [Matching](#matching) |
| Elastic IPs deleted days before, still in the Resource Explorer index | 2 | Resource Explorer-only records are trusted | [Resource Explorer](#resource-explorer) |

The other 24 are real or for the owner to judge: security group rules added by hand, a private
Route 53 record and two ACM validation records, ECR repositories, an S3 bucket and a log group
created by hand, IAM roles from the account vending process, and policies an AWS service attached
to a role a state manages (drift on a managed role, so it stays `unmanaged`).

### `undetermined` (650)

Every one is "no type mapping". Only the 56 curated types are matched ([matching.md](matching.md)),
and the generated CloudFormation candidate is only a hint.

- 252 are Kubernetes objects that Resource Explorer indexes for EKS clusters (`eks:deployment`,
  `eks:replicaset`, `eks:service`, `eks:endpointslice`, `eks:namespace`, `eks:daemonset`,
  `eks:ingress`, `eks:persistentvolume`, `eks:statefulset`).
- The rest are common Terraform types without a curated entry: SSM parameters (82), EKS Pod
  Identity associations (38), Lambda versions (36), event source mappings (24), EventBridge rules
  (21), CloudWatch alarms (16), API Gateway resources, VPC endpoints, the MSK cluster, the
  OpenSearch domain, the ElastiCache replication group, the EFS file system and others.
- Of 1,482 state resources of the account, 716 matched no live resource. Apart from types with
  no AWS resource (`terraform_data`, `random_*`, `helm_release` and the like), most are the same
  missing curated entries.

### Resources no layer listed (false negatives)

- VPC Lattice resource gateways and resource configurations: AWS Config doesn't record them and
  Resource Explorer doesn't index them. Cloud Control lists both.
- Types AWS Config records that scan dropped: ACM certificates, API Gateway domain names, EKS
  add-ons and node groups, VPC endpoint services, Route 53 Resolver rules, Backup recovery points,
  EC2 associations and account-level defaults.
- AWS Config records the OpenSearch domain as `AWS::OpenSearch::Domain`, Resource Explorer as
  `AWS::OpenSearchService::Domain`. The two names don't merge.

## Inheritance

A child that no state manages takes its parent's category when the parent is `aws-managed`,
`other-iac` or `derived`. Children are:

- IAM role, user and group policy attachments and inline policies (parent: the principal);
- S3 bucket subresources and the bucket policy (parent: the bucket);
- security group rules (parent: the group);
- apex NS and SOA records (parent: the hosted zone);
- the route table of a regional NAT gateway (parent: the NAT gateway);
- a VPC Lattice resource configuration of type `CHILD` (parent: its `ARN` configuration).

Rules:
- Classification gets a second pass after the state match, the CloudFormation stacks and the
  rules. It visits children whose category is `unmanaged` or `undetermined`, repeats until nothing
  changes, and stops at depth 3.
- The finding gets rule `inherit/<parent category>`, the parent's owner, and a reason that names
  the parent and its rule.
- A child of a `managed` parent stays `unmanaged`. An extra policy attached to a role a state
  manages is drift, and scan reports it.
- A child a user rule ignores stays ignored. `scan.never_hide` still applies to the child.
- `--explain` prints the parent chain.

## ENIs created by AWS services

`service-eni` becomes `service-eni` (category `derived`). It matches when any of these
holds:

- `requesterManaged` is true;
- `interfaceType` is one of `api_gateway_managed`, `aws_codestar_connections_managed`, `branch`,
  `ec2_instance_connect_endpoint`, `efs`, `gateway_load_balancer`,
  `gateway_load_balancer_endpoint`, `global_accelerator_managed`, `iot_rules_managed`, `lambda`,
  `load_balancer`, `nat_gateway`, `network_load_balancer`, `quicksight`, `transit_gateway`,
  `trunk`, `vpc_endpoint` (the EC2 API enum without `interface`, `efa`, `efa-only` and `evs`);
- `attachment.instanceOwnerId` is set and isn't the account (an AWS service account or
  `amazon-aws` owns the attachment);
- `description` starts with `AWS Lambda VPC ENI-`, `aws-K8S-`, `aws-k8s-branch-eni` or
  `aws-k8s-trunk-eni` (Lambda and the VPC CNI; unattached Lambda ENIs have no instance owner).

The config layer selects `configuration.description`, `configuration.requesterId` and
`configuration.attachment.instanceOwnerId` for `AWS::EC2::NetworkInterface`. The AWS Config
resource schema lists all three as queryable. `requesterId` is collected but no rule uses it yet:
on the validation account Lambda's value is a role of the account, so it doesn't tell a service
from a person. The owner decides after the second account.

## Kubernetes controllers

Rules in `other-iac`, owner named after the controller:

| Rule | Matches | Source |
|---|---|---|
| `external-dns` | A TXT record whose value starts with `heritage=external-dns`, and the record it owns | external-dns v0.24.0 `registry/mapper/mapper.go` |
| `aws-load-balancer-controller-rule` | A security group rule whose description starts with `elbv2.k8s.aws/`, or whose referenced group is tagged `elbv2.k8s.aws/cluster` | aws-load-balancer-controller v3.6.3 `pkg/networking` |
| `in-tree-elb-rule` | A rule whose description starts with `kubernetes.io/rule/nlb/` | cloud-provider-aws v1.37.0 |
| `vpc-cni-eni` | Tag `eks:eni:owner`, or `node.k8s.amazonaws.com/instance_id` | amazon-vpc-cni-k8s v1.23.3 |
| `ebs-csi` | Tag `ebs.csi.aws.com/cluster` | aws-ebs-csi-driver v1.68.0 |
| `efs-csi` | Tag `efs.csi.aws.com/cluster` | aws-efs-csi-driver v3.6.0 |
| `ack` | Tag `services.k8s.aws/controller-version` | ACK runtime v0.68.0 |

The EKS cluster security group (name `eks-cluster-sg-`, tag `aws:eks:cluster-name`) is
`aws-managed` with owner `eks` (rule `eks-cluster-security-group` in `aws-service`), so its rules
inherit.

external-dns ownership:
- The owned record isn't in the TXT value. scan strips the TXT name's prefix or suffix and the
  record-type token (`a`, `aaaa`, `cname`, `ns`, `mx`, `ptr`, `srv`, `naptr`, `txt`) from the first
  label, then looks up the record with that name, type and set identifier in the same zone. A
  `cname-` token also matches an A ALIAS record: external-dns used it for ALIAS records before
  v0.22.0.
- `scan.external_dns.txt_prefix` and `txt_suffix` hold the deployment's settings. Empty means try
  no prefix, then `external-dns-`.
- An encrypted TXT value (`--txt-encrypt-enabled`) can't be read, so only the TXT name is used for
  it.
- The Route 53 lister reads the value of TXT records only, to check the `heritage=` prefix. It
  stores the result as a boolean attribute, never the value.

## AWS defaults

| Rule | Matches |
|---|---|
| `apex-ns-soa` | The NS and SOA records at a hosted zone's apex. Route 53 creates them with every zone. |
| `default-dhcp-options` | DHCP options with only `domain-name` set to `<region>.compute.internal` (`ec2.internal` in us-east-1) and `domain-name-servers` set to `AmazonProvidedDNS`, and no tags. EC2 has no default flag, so the config layer adds `configuration.dhcpConfigurations`. |
| `default-security-group-rules` | Covered by inheritance from `default-security-group`. |
| `main-route-table` | A route table with an association `main: true`, unless a state manages it (`aws_default_route_table`). AWS creates it with every VPC. The config layer adds `configuration.associations`. |
| `regional-nat-route-table` | The route table whose ID is a regional NAT gateway's `routeTableId`. AWS creates it with the gateway. AWS Config doesn't record the field, so the EC2 native lister reads `ec2:DescribeNatGateways`. |
| `account-defaults` | Resources every account has once the type is curated: CodeDeploy `CodeDeployDefault.*` configurations, AppConfig `AppConfig.*` deployment strategies, the Amazon Keyspaces `system*` keyspaces, the ECS `FARGATE` and `FARGATE_SPOT` capacity providers, the EventBridge Scheduler `default` group, the Route 53 Resolver auto-defined rules, and the account-level EC2 and ECR settings. Check each name on the validation account before writing it into the pack. |

## AWS services

Rules in `aws-service` (category `aws-managed`):

| Rule | Matches |
|---|---|
| `control-tower` (widened) | Names starting with `AWSControlTower` as well as `aws-controltower-`, for every type |
| `aft` | Roles `AWSAFTAdmin`, `AWSAFTExecution`, `AWSAFTService`, and tag `managed_by=AFT` (category `other-iac`, owner `aft`) |
| `quicksight-service-role` | IAM roles and policies under path `/service-role/` named `aws-quicksight-*` or `AWSQuickSight*` |
| `apigateway-log-group` | Log groups `API-Gateway-Execution-Logs_<api>/<stage>` and `/aws/apigateway/welcome` |
| `service-linked-secret` | Secret names `<service>!...` for the documented service IDs (`appflow`, `databrew`, `datasync`, `directconnect`, `ecs-sc`, `events`, `marketplace-deployment`, `opsworks-cm`, `pcs`, `rds`, `redshift`, `sqlworkbench`) and `s2svpn` (seen on the validation account), or tag `aws:secretsmanager:owningService`. AWS Config doesn't record `OwningService`. |
| `log-delivery-bucket-policy` | A bucket policy whose statement IDs are all `AWSLogDeliveryWrite*` or `AWSLogDeliveryAclCheck*`. The S3 lister stores the statement IDs only, never the policy. |
| `lambda-version` (`derived`) | A Lambda version of a function a state manages. `publish = true` creates versions that no state lists. |
| `rds-automated-snapshot` (`derived`) | RDS snapshots of type `automated` and automated backups |
| `backup-recovery-point` (`derived`) | Backup recovery points of a vault a state manages |
| `addon-pod-identity-association` (`derived`) | An EKS Pod Identity association with `ownerArn` set (an add-on owns it) |

## Matching

- `aws_iam_policy_attachment` gets a curated entry for `Native::IAM::RolePolicyAttachment`,
  `UserPolicyAttachment` and `GroupPolicyAttachment`. The state reader expands its `roles`,
  `users` and `groups` with its `policy_arn` into one key per principal, like the rule-tuple keys of
  `aws_security_group_rule`.
- `aws_default_route_table`, `aws_default_security_group`, `aws_default_network_acl` and
  `aws_default_vpc_dhcp_options` get curated entries next to their non-default types.
- The curated typemap covers every Terraform type in the table below. The generator's
  `guessCFN` gets an alias table for names that differ between the two providers
  (`aws_cloudwatch_event_*` and `aws_events_*`, `aws_cloudwatch_metric_alarm` and
  `aws_cloudwatch_alarm`, `aws_api_gateway_*` and `aws_apigateway_*`, `aws_opensearch_domain` and
  `aws_opensearchservice_domain`, `aws_codepipeline` and `aws_codepipeline_pipeline`).
- One live resource has one CloudFormation type. `AWS::OpenSearchService::Domain` (Resource
  Explorer, CloudFormation) and `AWS::OpenSearch::Domain` (AWS Config) map to one entry, and so do
  the other pairs where Config and CloudFormation names differ.

Types to curate, from the validation account's state and live listing:

| Service | Terraform types |
|---|---|
| EC2 and VPC | `aws_vpc_endpoint`, `aws_vpc_endpoint_service`, `aws_route_table_association`, `aws_network_acl_association`, `aws_vpc_ipv4_cidr_block_association`, `aws_vpc_peering_connection`, `aws_vpc_peering_connection_accepter`, `aws_flow_log`, `aws_ec2_managed_prefix_list`, `aws_ec2_client_vpn_endpoint`, `aws_customer_gateway`, `aws_vpn_gateway`, `aws_vpn_connection`, `aws_eip_association` |
| VPC Lattice | `aws_vpclattice_resource_gateway`, `aws_vpclattice_resource_configuration`, `aws_vpclattice_service_network`, `aws_vpclattice_service_network_resource_association`, `aws_vpclattice_service_network_vpc_association` |
| EKS | `aws_eks_node_group`, `aws_eks_addon`, `aws_eks_access_entry`, `aws_eks_pod_identity_association`, `aws_iam_openid_connect_provider` |
| Lambda and events | `aws_lambda_event_source_mapping`, `aws_lambda_layer_version`, `aws_cloudwatch_event_rule`, `aws_cloudwatch_event_bus`, `aws_pipes_pipe`, `aws_scheduler_schedule_group` |
| Data | `aws_msk_cluster`, `aws_msk_configuration`, `aws_opensearch_domain`, `aws_elasticache_replication_group`, `aws_elasticache_user`, `aws_elasticache_user_group`, `aws_memorydb_acl`, `aws_memorydb_user`, `aws_memorydb_parameter_group`, `aws_db_parameter_group`, `aws_db_option_group`, `aws_efs_file_system`, `aws_efs_access_point`, `aws_efs_mount_target`, `aws_glue_catalog_database`, `aws_glue_catalog_table`, `aws_athena_workgroup`, `aws_athena_data_catalog` |
| API Gateway | `aws_api_gateway_rest_api`, `aws_api_gateway_resource`, `aws_api_gateway_method`, `aws_api_gateway_deployment`, `aws_api_gateway_stage`, `aws_api_gateway_domain_name`, `aws_api_gateway_base_path_mapping` |
| Other | `aws_ssm_parameter`, `aws_ssm_association`, `aws_cloudwatch_metric_alarm`, `aws_cloudwatch_dashboard`, `aws_acm_certificate`, `aws_codebuild_project`, `aws_codepipeline`, `aws_kinesisanalyticsv2_application`, `aws_backup_plan`, `aws_backup_vault`, `aws_backup_selection`, `aws_cloudformation_stack`, `aws_quicksight_data_source`, `aws_quicksight_vpc_connection`, `aws_route53_resolver_rule`, `aws_route53_resolver_rule_association`, `aws_xray_group`, `aws_xray_sampling_rule`, `aws_resourceexplorer2_index`, `aws_resourceexplorer2_view`, `aws_apprunner_auto_scaling_configuration_version` |

A type whose live resource has no AWS Config, Resource Explorer or Cloud Control listing can't be
matched. Its entry says so, and the type stays out of the coverage test.

## Resource Explorer

- The layer sets the resource ID from the ARN captures, as Config and Cloud Control do.
- Resource Explorer is eventually consistent (its FAQ: replication can take up to 36 hours). A
  record only Resource Explorer lists, of a type the config layer listed in the same account and
  region, is dropped. Config is authoritative for the types it records. The run counts dropped
  records in the coverage notes.
- The Kubernetes object types in the table above (`eks:deployment` and the others) are skipped.
  They are not AWS infrastructure, and the Terraform AWS provider has no resource for them. The
  run counts them in the coverage notes. `eks:cluster` and `eks:podidentityassociation` stay.

## Cloud Control for unlisted types

- Curated entries can set `cc: true` for types that neither AWS Config records nor Resource
  Explorer indexes. Those types join `scan.cloudcontrol_types` by default, so `--layers auto`
  lists them.
- The first set: `AWS::VpcLattice::ResourceGateway`, `ResourceConfiguration`,
  `ServiceNetworkResourceAssociation` and `ServiceNetworkVpcAssociation`. SSM parameters, event
  source mappings and pipes, which AWS Config doesn't record either, come from Resource Explorer.
- For a resource configuration the layer reads `ResourceConfigurationType` (and `AmazonManaged`
  where the API returns it) so the `CHILD` rule can apply.
- Every type in the set adds one paginated `ListResources` call per region at the Cloud Control
  budget (2 rps).

## Coverage report

`scan --explain-coverage` gains a section that lists the resource types AWS Config recorded in
each account and region that scan doesn't map, with counts, from
`config:GetDiscoveredResourceCounts` (covered by `config:Get*` in the read-only policy). This is
how the next accounts show what is still missing.

## Measuring accuracy

- `docs/validation/nonprod-scan.md` holds one row per scanned account: the date, the tfcensus
  version, the resource count, the `unmanaged` count, the false positives by group, the
  false-positive rate, the `undetermined` count and the false negatives found. Accounts are
  numbered, never named.
- The reviewer adds a `verdict` column (`fp`, `tp` or `unsure`) to the scan's CSV.
  `mise run validation:stats -- reviewed.csv` prints the row: counts by category, verdict and
  type, with no IDs, names or ARNs.
- Each false-positive group above becomes a fixture in `testdata/validation/` built from
  real-shaped records with every ID, name, ARN, tag value and account replaced. A testscript scans
  it with stubbed AWS and checks that none of it is `unmanaged`.

## Acceptance criteria
1. [ ] Inheritance: a table test covers every child kind above, the three inheriting parent
   categories, a `managed` parent (child stays `unmanaged`), depth 3, an ignored child and a
   `never_hide` guard. `--explain` prints the parent chain (golden).
2. [x] `service-eni` has positive fixtures for each condition and negatives for an unattached
   user-created ENI and an EC2 instance's primary ENI. The config query cassettes include the
   three new properties.
3. [ ] The Kubernetes controller rules, the AWS default rules and the AWS service rules above
   each have positive and negative fixtures (`TestSpec_ownership_1` covers the packs). The
   external-dns test covers prefix, suffix, the legacy `cname-` token on an A ALIAS record, the
   `%{record_type}` template and an encrypted value.
4. [ ] `aws_iam_policy_attachment` with two roles and a user matches three live attachments.
   `aws_default_route_table` matches the main route table. Match tests cover both.
5. [ ] Every Terraform type in the table above is curated or carries the reason it can't be
   listed. A test fails when one is missing, and the generator's alias table gives the expected
   `cfn:` for the five renamed families.
6. [ ] Resource Explorer: an RE-only record of a Config-listed type is dropped and counted, an RE
   record has an `id`, the Kubernetes object types are skipped and counted.
7. [ ] Cloud Control lists the `cc: true` types by default. A cassette lists a resource gateway
   and an `ARN` resource configuration with its `CHILD`, and the child is `derived`.
8. [ ] `--explain-coverage` lists unmapped Config types with counts (cassette and golden).
9. [ ] The validation fixtures for all 13 false-positive groups scan with no `unmanaged`
   finding, and `TestSpec_accounts_4` still passes, so no new IAM action is needed.
10. [ ] A read-only rerun on the validation account with the same command gives under 5%
    false positives among `unmanaged` findings, no `undetermined` finding of a curated type, and
    lists the VPC Lattice resources and ACM certificates that v0.3.0 missed.
    `docs/validation/nonprod-scan.md` records the before and after rows, and
    ownership-rules.md criterion 5 is ticked.
