# Spec: matching live resources to state (typemap + match)

## Match keys, in priority order
1. **Resource identity** (TF/Tofu 1.12+ state `identity`). In terraform-provider-aws (main, 6.72) **578 of 1,761 resources (~33%)** have identity:
   - RegionalSingleParameter 332, RegionalParameterized 192, RegionalARN 220, Global* 72, Singleton 33.
   - The identity is canonicalized with account and region and compared to the live resource's identifier.
2. **ARN**: state `arn` attribute vs the live ARN (exact, after partition normalization).
3. **Normalized ID**: per TF type, an `id` normalizer from typemap. Examples:
   - `aws_iam_role_policy_attachment` `role/arn` → composite
   - `aws_route53_record` `ZONE_name_TYPE[_setid]`
   - `aws_security_group_rule` `sgrule-<hash>` is synthetic, so it's matched by expanding the rule tuple (protocol, ports, cidr/sg, direction) instead.

## typemap (data, not code)
`internal/typemap/data/*.yaml`, generated plus curated:

```yaml
- tf: aws_lb
  cfn: AWS::ElasticLoadBalancingV2::LoadBalancer
  arn: "arn:{partition}:elasticloadbalancing:{region}:{account}:loadbalancer/{type}/{name}/{id}"
  id: arn                       # TF id equals ARN
- tf: aws_security_group
  cfn: AWS::EC2::SecurityGroup
  id: attr:id                   # sg-…
- tf: aws_vpc_security_group_ingress_rule
  cfn: AWS::EC2::SecurityGroupIngress
  native: ec2-sg-rules
  id: attr:security_group_rule_id
```

Sources for generating it:
- `terraform providers schema -json` (identity schemas, attribute names), run against pinned provider versions in a `mise run typemap:gen` task.
- **awscc's `all_schemas.hcl`** (MPL-2.0) as the CFN type universe.
- Curated aws→CFN pairs seeded by name similarity + ARN resource-type patterns. About 300 types cover most real estates; the rest are reported as `undetermined: no type mapping`.

The typemap carries `min_provider_version` / `max_provider_version` where ID formats changed.

## Algorithm
- Build the StateIndex keys for every managed instance, and the live keys for every CloudResource (all candidate TF types).
- Join: identity → ARN → normalized ID.
  - One match: managed.
  - More than one match from different units: double-managed (also reported by audit and whois).
  - No match: hand the resource to ownership rules ([ownership-rules.md](ownership-rules.md)).
- **Parent-in-state inheritance:** if a resource's parent (relationship from Config, or an ARN prefix) is managed, and the type is a known derived child, the category is `derived` with the reason "child of managed <parent address>".
- Never claim a resource is managed when its type has no mapping. It goes to `undetermined` with the reason, and those counts show in the summary.

## Acceptance criteria
- [ ] Table tests for the 40 most common types (VPC/subnet/SG/rules, IAM role/policy/attachment, S3 + subresources, KMS, RDS, EKS, ELBv2 + listeners/target groups, Route53, Lambda, SQS/SNS, ECR, Secrets Manager, CloudWatch logs, DynamoDB).
- [ ] Identity-based and ARN-based matching give the same result on a fixture with both.
- [ ] SG rule tuple matching handles inline, legacy `aws_security_group_rule` and the new per-rule resources.
- [ ] A typemap generation task is reproducible (same input → byte-identical output).
- [ ] The provider source paths are parameters (`TFCENSUS_PROVIDER_AWS_DIR`, `TFCENSUS_PROVIDER_AWSCC_DIR`) with `_reference` defaults; the generated header records the source commits, and no machine-specific path is committed.
