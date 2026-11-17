---
description: The eight categories a scan puts each live AWS resource in, how a child resource takes its parent's category, and how the coverage percentage is counted.
---

# Scan categories

`scan` puts every live resource it lists in exactly one category. The category says who owns the
resource: a Terraform state, AWS, another tool, or nobody. This page explains each category, the
order in which they're decided, how a child takes its parent's category, and what the coverage
percentage counts.

## The categories

| Category | Meaning | Decided by |
|---|---|---|
| `managed` | One state manages it. | [matching](/concepts/matching) |
| `double-managed` | More than one state manages it. | matching |
| `unmanaged` | No state manages it and no ownership rule matches it. | no match anywhere |
| `other-iac` | CloudFormation, CDK, Pulumi, Crossplane or a Kubernetes controller owns it. | a CloudFormation stack, or a rule |
| `aws-managed` | AWS created it, such as the default VPC or a service-linked role. | a rule |
| `derived` | Another resource creates it, such as an instance's root volume. | matching or a rule |
| `ignored` | A rule hides it on purpose. | a rule |
| `undetermined` | Its type has no Terraform mapping, so tfcensus can't tell. | matching |

`unmanaged` is the category to act on: adopt the resource with [codify](/guides/codify), delete
it, or write a rule that says who owns it ([rules guide](/guides/rules)). `--fail-on unmanaged`
gates CI on it ([scan guide](/guides/scan)).

## The order of decisions

Each resource goes through these steps, and the first that decides wins:

1. Matching. A state match makes it `managed` or `double-managed`. A managed parent makes it
   `derived`. An unmapped type makes it `undetermined`, but it still goes through steps 2 and 3,
   because a stack or a rule can still name its owner.
2. CloudFormation. With `scan.cloudformation` on (the default), a resource that a CloudFormation
   stack lists is `other-iac`, with rule `source/cloudformation` and the stack in the finding. A
   resource a state manages stays `managed`, and the report warns that a stack lists it too.
3. Ownership rules. The built-in packs, then your rules, first match wins. A matching rule sets
   the category, the owner and the reason ([rules model](/concepts/rules)).
4. Nothing matched. A mapped type is `unmanaged` with rule `rules/no-match`. An unmapped type stays
   `undetermined`.
5. Inheritance, described below.

Every finding carries the `rule` that decided it and a `reason` in words. The
[rule IDs](/reference/rule-ids) page lists them all.

## Inheritance

Some resources are parts of another resource: a policy attachment of a role, a bucket policy, a
security group rule. When the parent belongs to AWS or another tool, so do its parts. After the
rules ran, scan makes a second pass: a child that is still `unmanaged` or `undetermined` takes the
category of an `aws-managed`, `other-iac` or `derived` parent. Its rule becomes
`inherit/<category>`, such as `inherit/aws-managed`, and its `parent` field names the parent and
the rule that decided the parent.

| Child | Parent |
|---|---|
| role, user and group policy attachments and inline policies | the IAM role, user or group |
| bucket policy, versioning, encryption, lifecycle and public access block | the S3 bucket |
| security group ingress and egress rules | the security group |
| NS and SOA records at a hosted zone's apex | the hosted zone |
| route tables that a regional NAT gateway creates | the NAT gateway |
| child VPC Lattice resource configurations | the parent resource configuration |

So the inline policy of a service-linked role is `aws-managed` because the role is, and the rule
of a security group that CloudFormation owns is `other-iac`. A chain passes down up to three
parents deep.

A child of a `managed` parent stays `unmanaged`. Something attached by hand to a resource Terraform
manages is drift, and the report should show it.

`scan.never_hide` limits inheritance too. When the parent's category is in `never_hide` and a guard
matches the child or the parent, the child keeps its own category
([guards](/concepts/rules#guards)).

`scan --explain` prints the parent chain of one resource:

```sh
tfcensus scan --profile acme-nonprod --explain arn:aws:iam::111111111111:role/app
```

## Coverage

The scan summary shows a coverage percentage per account and region:

```text
coverage = (managed + double-managed) / (managed + double-managed + unmanaged)
```

`aws-managed`, `other-iac`, `derived`, `ignored` and `undetermined` resources don't count on either
side. Coverage answers "of the resources someone should manage with Terraform, how many does a
state manage?". It is empty (`-`, or `null` in JSON) when an account and region have neither
managed nor unmanaged resources.

Coverage counts only what the [inventory layers](/concepts/inventory-layers) listed. A type no
layer covers is missing from both sides, so check `--explain-coverage` before you trust a high
number.
