# Spec: ownership rules (noise filtering)

Every unmatched or undetermined live resource is evaluated against ordered rules
([ADR-0010](../adr/0010-rules-on-undetermined-and-guards.md)). The **first match** sets
`{category, owner, reason, rule}`. If nothing matches, an unmatched resource is `unmanaged` and an
undetermined one stays `undetermined`. Order: the packs in the table's order, then the config's
rules, then each `--rules` file's.
Rules are auditable: `scan --explain <arn>` prints the evaluation trace.

## Built-in rule packs (on by default, individually switchable)

| Pack | Category | Matches (examples) |
|---|---|---|
| `aws-defaults` | aws-managed | default VPC, subnets, IGW, route tables, NACLs, SG `default`, DHCP options (unless in state) |
| `aws-service` | aws-managed | IAM roles under `/aws-service-role/`, `AWSReservedSSO_*`, `aws-controltower-*`, `stacksets-exec-*`; KMS keys with KeyManager=AWS and `alias/aws/*`; CDK bootstrap resources |
| `other-iac` | other-iac | tags `aws:cloudformation:stack-name`/`stack-id` (CFN, CDK, StackSets, Control Tower), `pulumi:*`, `crossplane.io/*` and `crossplane-*` (what the Crossplane AWS provider sets), `kubernetes.io/cluster/*`, `elbv2.k8s.aws/*`, `ingress.k8s.aws/*` |
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

- Expressions use **CEL** (`cel.dev/cel-go`; the same as Kubernetes ValidatingAdmissionPolicy). Variables: `resource` (account, region, type, source_type, tf_types, arn, id, name, tags, attrs, layer, sources, parents, relationships, created) and `unit` (repo, path, workspace, address; empty for unmanaged resources). Each parent and relationship has type, id, arn, name, relationship, tags, attrs and found (the inventory has it). A missing map key is an error that counts as no match, so write `"owner" in resource.tags` or `resource.tags.?owner.orValue("")`. `in_state(tf_type, id)` reports whether a state of the run manages that resource. The CEL string extensions are on.
- The packs live in `internal/ownership/packs/*.yaml` and compile like user rules. Rule IDs are `pack/<pack>/<name>` and `user/<name>`; a resource no rule matches carries `rules/no-match`.
- `scan.never_hide: [category…]` stops user rules from hiding guarded resources (guardrails borrowed from tgsieve). Guards are built in: every IAM user, since AWS Config doesn't record console access. With `never_hide: [ignored]`, no user rule can ignore an IAM user.
- `tfcensus rules test rules.yaml --resource sample.json` evaluates the rules offline.

## Acceptance criteria
- [x] Every built-in pack has fixtures (positive and negative) from real-shaped Config items.
- [x] Expired rules stop matching and produce a warning (injected clock).
- [x] `--explain` trace golden output.
- [x] The rules schema in `schemas/config-v1.json`; CEL compile errors report rule name and column.
- [ ] Measured: a non-prod account scan with built-in packs only yields under 5% false positives (manual review recorded in `docs/validation/nonprod-scan.md`, aggregated only).
