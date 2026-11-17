---
description: How scan and whois join a live AWS resource to a resource in state, by identity, then ARN, then normalized ID, and why an unmapped type is undetermined instead of managed.
---

# Matching

Matching decides whether a state manages a live resource. `scan` matches every resource an
[inventory layer](/concepts/inventory-layers) listed against the state index, and `whois` uses
the same keys to find the units that manage an ARN or ID. Every result carries the rule that
decided it, listed on the [rule IDs](/reference/rule-ids) page.

## The type mapping comes first

AWS Config, Resource Explorer and Cloud Control name types the CloudFormation way
(`AWS::EC2::SecurityGroup`). State names them the Terraform way (`aws_security_group`). tfcensus
carries a type mapping between the two, with the keys each Terraform type can be matched by.

A live resource whose type has no mapping is `undetermined` (rule `match/no-type-mapping`).
tfcensus can't tell which Terraform type to look for, so it never guesses that a state manages
the resource, and it never calls it unmanaged either. The
[type mapping coverage](/reference/type-coverage) page lists every mapped type.

## Three keys, strongest first

For each Terraform type a live resource can be, tfcensus builds up to three keys, and builds the
same keys for each resource in state. The first kind of key that finds a state resource decides:

1. Identity (`match/identity`). The resource identity: the account, the region and the attributes
   the type is identified by, such as a role's name. A state written by terraform or OpenTofu
   1.12 or later stores these in an identity block. For older states, tfcensus reads the same
   attributes from the resource's attributes.
2. ARN (`match/arn`). The resource's ARN, as state stores it. CloudWatch Logs reports a log group
   ARN with a trailing `:*`, which the AWS provider leaves out, so tfcensus drops it before it
   compares.
3. Normalized ID (`match/id`). The account, the region and the Terraform `id`. For the live side,
   tfcensus renders the `id` the provider would store from the resource's fields, such as
   `role-name:policy-name` for an inline role policy.

A global type, such as an IAM role or a Route 53 record, has no region in any of its keys.

Only these keys decide. tfcensus doesn't match by name or tag, because two resources can share a
name and tags are easy to copy.

## Security group rules

An `aws_vpc_security_group_ingress_rule` or `aws_vpc_security_group_egress_rule` stores the
rule's own `sgr-` ID, so it matches by ID. An `aws_security_group_rule` and the inline rules of an
`aws_security_group` don't, so tfcensus matches those by the rule's tuple: the group, direction,
protocol, ports and source. An `aws_security_group_rule` with the same tuple matches first
(`match/rule-tuple`). An inline `ingress` or `egress` block matches only when no
`aws_security_group_rule` has the tuple (`match/inline-rule`).

## Managed, double-managed, derived

- One state has the resource: `managed`. The finding's `managed_by` names the unit, workspace and
  address.
- More than one state has it: `double-managed` (`match/double-managed`). Two states that both apply
  the same resource fight over it, so `scan --fail-on double-managed` can gate CI on this.
- No state has it, but a state manages its parent, which creates it: `derived`
  (`match/parent-in-state`). An example is the root `/` resource of a REST API whose
  `aws_api_gateway_rest_api` is in state.

Anything else is unmatched and goes on to the [ownership rules](/concepts/rules), which decide
whether it is `unmanaged`, owned by AWS, owned by another tool, or ignored. The
[categories](/concepts/categories) page describes the result.

## See one decision

`scan --explain` prints how one resource was categorized: the match result, the outcome of each
ownership rule that ran, and the parent chain.

```sh
tfcensus scan --profile acme-nonprod --explain arn:aws:ec2:eu-west-1:111111111111:security-group/sg-0123456789abcdef0
```

`whois` answers the reverse question from the state side, offline from a saved index too
([whois guide](/guides/whois)).
