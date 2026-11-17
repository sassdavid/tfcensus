---
description: The fields and functions an ownership rule's match expression can use, with the CEL syntax for tags, attributes and related resources.
---

# CEL fields and functions for rules

An ownership rule's `match` is a [CEL](https://cel.dev) expression that returns a boolean. `scan`
evaluates it for each resource that no state manages, and `whois --live` and `codify --select` use
the same variables. The built-in [rule packs](/reference/rule-packs) are written the same way.

```yaml
rules:
  - name: datadog-integration
    category: other-iac
    owner: datadog
    match: 'resource.type == "AWS::IAM::Role" && resource.name.startsWith("DatadogIntegrationRole")'
    reason: created by the Datadog CloudFormation quick start
  - name: sandbox-experiments
    category: ignored
    match: 'resource.account == "111111111111" && resource.tags.?owner.orValue("") == "sandbox"'
    reason: temporary experiments, review in Q1
    expires: "2027-03-31"
```

## Variables

A rule sees two variables: `resource`, the live resource, and `unit`, the unit whose state manages
it. `unit` is empty for an unmanaged resource, so most rules read `resource` only. Each entry of
`resource.parents` and `resource.relationships` is a Related object.

<!--@include: ../parts/cel-fields.md-->

## Functions

| Function | Returns |
|---|---|
| `in_state(tf_type, id)` | `true` when a state in the run manages a resource of Terraform type `tf_type` with that ID, such as `in_state("aws_lambda_function", resource.name)`. |
| The CEL standard library | comparisons, `in`, `size`, `startsWith`, `endsWith`, `contains`, `matches` (RE2), and the macros `exists`, `all`, `exists_one`, `map` and `filter`. |
| The CEL strings extension | `lowerAscii`, `upperAscii`, `split`, `replace`, `substring`, `trim`, `indexOf`, `join` and the others of [cel-go `ext.Strings`](https://pkg.go.dev/cel.dev/cel-go/ext#Strings). |
| Optional values | `map.?key` gives an optional value, and `.orValue(default)` reads it with a default. |

## Missing keys

Reading a map key that doesn't exist, such as `resource.tags["owner"]` on an untagged resource, is
an evaluation error, and the rule doesn't match. Test the key first, or read it as an optional
value:

```text
"owner" in resource.tags && resource.tags["owner"] == "sandbox"
resource.tags.?owner.orValue("") == "sandbox"
resource.relationships.exists(r, r.type == "AWS::EC2::VPC" && r.attrs.?is_default.orValue("") == "true")
```

A rule whose expression doesn't compile, or doesn't return a boolean, fails the run with exit 2,
and the error names the rule and the column. `tfcensus rules test` checks rules against a sample
resource without calling AWS.
