# Spec: ownership rules (noise filtering)

Every unmatched live resource is evaluated against ordered rules. The **first match** sets
`{category, owner, reason, rule}`. If nothing matches, the resource is `unmanaged`.
Rules are auditable: `scan --explain <arn>` prints the evaluation trace.

## Built-in rule packs (on by default, individually switchable)

| Pack | Category | Matches (examples) |
|---|---|---|
| `aws-defaults` | aws-managed | default VPC, subnets, IGW, route tables, NACLs, SG `default`, DHCP options (unless in state) |
| `aws-service` | aws-managed | IAM roles under `/aws-service-role/`, `AWSReservedSSO_*`, `aws-controltower-*`, `stacksets-exec-*`; KMS keys with KeyManager=AWS and `alias/aws/*`; CDK bootstrap resources |
| `other-iac` | other-iac | tags `aws:cloudformation:stack-name`/`stack-id` (CFN, CDK, StackSets, Control Tower), `pulumi:*`, `crossplane.io/*`, `kubernetes.io/cluster/*`, `elbv2.k8s.aws/*`, `ingress.k8s.aws/*` |
| `derived` | derived | ENIs with RequesterManaged/InterfaceType (lambda, nat_gateway, vpc_endpoint, network_load_balancer, eks); instances with `aws:autoscaling:groupName`, `eks:nodegroup-name`, `karpenter.sh/*`; EKS/Karpenter launch templates; ECS tasks; Lambda@Edge replicas; EBS volumes attached to derived instances; snapshots created by AWS Backup (`aws:backup:source-resource`) |
| `ephemeral` | ignored | CloudWatch log groups `/aws/lambda/*` whose function is managed (parent-in-state), Athena query results, etc. |

## User rules (`.tfcensus.yaml` → `rules:`)

```yaml
rules:
  - name: datadog-integration
    category: other-iac
    owner: datadog
    match: 'resource.type == "AWS::IAM::Role" && resource.name.startsWith("DatadogIntegrationRole")'
    reason: Created by Datadog CloudFormation quick-start
  - name: sandbox-experiments
    category: ignored
    match: 'resource.account == "123456789012" && resource.tags["owner"] == "sandbox"'
    expires: 2027-03-31        # after this date the rule stops matching and a warning names it
    reason: Temporary experiments, review in Q1
```

- Expressions use **CEL** (google/cel-go; the same as Kubernetes ValidatingAdmissionPolicy). Variables: `resource` (account, region, type, tf_types, arn, id, name, tags, layer, relationships) and `unit` (when one is matched).
- `never_hide: [category…]` at the top level stops user rules from hiding things (e.g. `ignored` can never apply to IAM users with console access; guardrails borrowed from tgsieve).
- `tfcensus rules test rules.yaml --resource sample.json` evaluates the rules offline.

## Acceptance criteria
- [ ] Every built-in pack has fixtures (positive and negative) from real-shaped Config items.
- [ ] Expired rules stop matching and produce a warning (injected clock).
- [ ] `--explain` trace golden output.
- [ ] The rules schema in `schemas/config-v1.json`; CEL compile errors report rule name and column.
- [ ] Measured: a non-prod account scan with built-in packs only yields under 5% false positives (manual review recorded in `docs/validation/nonprod-scan.md`, aggregated only).
