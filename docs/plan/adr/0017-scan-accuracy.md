# tfcensus ADR-0017: Scan accuracy from the first real-account run

- Status: proposed (2026-11-16). Accepted when M21 starts. Builds on
  [ADR-0003](0003-capability-probed-layered-inventory.md) (inventory layers),
  [ADR-0004](0004-identity-arn-id-matching.md) (matching),
  [ADR-0005](0005-cel-ownership-rules.md) (rules),
  [ADR-0010](0010-rules-on-undetermined-and-guards.md) (rules on undetermined resources) and
  [ADR-0014](0014-cloudformation-stack-source.md) (CloudFormation stacks).
  Spec: [scan-accuracy.md](../specs/scan-accuracy.md).

## Context
- A read-only v0.3.0 scan of a real non-prod account reported 171 `unmanaged` resources, of
  which 147 were false (86%). The target in ownership-rules.md criterion 5 is under 5%.
- Most false findings are children of resources scan already classified correctly: attachments of
  service-linked roles, rules of the EKS cluster security group, subresources of a bucket a stack
  created. Classification is one pass per resource, so a child never sees its parent's category.
- The ENI rule reads two flags. The services that created 27 ENIs set neither.
- The config layer reads five configuration flags and never a value a user wrote
  (inventory.md, `internal/inventory/config.go`). The ENI rule needs the description, the
  external-dns rule needs a TXT value, and the log-delivery rule needs a bucket policy's statement
  IDs.
- All 650 `undetermined` findings were "no type mapping", 252 of them Kubernetes objects that
  Resource Explorer indexes for EKS clusters.
- Resource Explorer is eventually consistent. Two deleted Elastic IPs were still in its index.
- AWS Config and Resource Explorer don't list VPC Lattice resource gateways and resource
  configurations. Cloud Control does, but its layer runs only for an allowlist that is empty by
  default.

## Decision
- Classification gets a second pass. A child that is `unmanaged` or `undetermined` after the
  state match, the stacks and the rules takes its parent's category when that is `aws-managed`,
  `other-iac` or `derived`, with rule `inherit/<category>`. A child of a `managed` parent stays
  `unmanaged`, because a resource added to a managed one is drift. `scan.never_hide` applies to
  the child. `--explain` runs the same pass and prints the parent chain.
- Inventory may read a user-written value when a rule needs it to tell a service from a person.
  The value goes into `resource.attrs` for rules, is never printed in tty, md, csv or sarif, and is
  reduced where possible: the TXT value becomes a boolean (`heritage=external-dns` or not) and a
  bucket policy becomes its statement IDs. The ENI description is kept as is, since its prefix is
  the signal. Secret values, policy documents and template bodies stay out.
- A record that only Resource Explorer lists is dropped when its type is one the config layer
  listed in the same account and region. Config is authoritative for what it records.
- Resource Explorer's Kubernetes object types (`eks:deployment` and the others in the spec) are
  skipped and counted. They are not AWS infrastructure and no Terraform AWS resource manages them.
- A curated type may set `cc: true` when neither AWS Config nor Resource Explorer lists it. Those
  types join `scan.cloudcontrol_types` by default.
- The curated typemap grows to every type the validation account uses (the table in the spec),
  and a test keeps that list curated. Further accounts add their types the same way.

## Consequences
- inventory.md's "five configuration flags" sentence and the comment in `config.go` change to the
  rule above. The config query cassettes change.
- Parents must be resolvable for every child kind. The native listers already set them for IAM
  attachments, SG rules, Route 53 records and S3 subresources. The regional NAT route table and the
  `CHILD` resource configuration need new parent links.
- Scan makes more calls: one `GetDiscoveredResourceCounts` per account and region for the
  coverage report, one `DescribeNatGateways` per region, and one Cloud Control `ListResources` per
  `cc: true` type and region. All are in the read-only policy.
- A user who relied on `unmanaged` children of `aws-managed` parents to see them now finds them
  under that category. `--explain` shows why.
