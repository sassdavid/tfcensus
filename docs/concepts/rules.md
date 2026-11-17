---
description: How ownership rules decide who owns a resource no state manages, with built-in packs, your own CEL rules, guards and never_hide, where the first rule that matches wins.
---

# Ownership rules model

A resource that no state manages isn't always a problem. AWS creates default VPCs and
service-linked roles, other tools create their own resources, and some resources are by-products
of others. Ownership rules say which is which. This page explains how they run. The
[rules guide](/guides/rules) shows how to write and test your own, and the
[CEL reference](/reference/cel) lists every field a rule can read.

## What a rule is

A rule has a name, a category, a CEL expression in `match`, a `reason`, and optionally an `owner`
and an `expires` date:

```yaml
rules:
  - name: datadog-integration
    category: other-iac
    owner: datadog
    match: 'resource.type == "AWS::IAM::Role" && resource.name.startsWith("DatadogIntegrationRole")'
    reason: created by the Datadog CloudFormation quick start
```

The category is one of `other-iac`, `aws-managed`, `derived` or `ignored`. A rule can't make a
resource `managed`: only a state match does that. The expression must return a bool. It reads the
live resource as `resource` (type, ARN, ID, name, tags, attributes, parents and related
resources), and can call `in_state(tf_type, id)` to ask whether a state manages another resource.

## Where rules run

Rules run on every resource that [matching](/concepts/matching) left unmatched, and on
`undetermined` resources, after the CloudFormation step ([order of decisions](/concepts/categories#the-order-of-decisions)).
A resource a state manages never reaches them. `whois` runs the same rules on an ARN that no
state manages, and `codify --select` filters findings with an expression over the same `resource`
variable.

## First match wins

Rules run in a fixed order, and the first rule whose expression is true decides the category,
owner and reason. The finding's `rule` names it. The order is:

1. The built-in packs, in this order: `aws-defaults`, `aws-service`, `other-iac`, `derived`,
   `ephemeral`. Their rules carry IDs such as `pack/aws-defaults/default-vpc`.
2. Your rules from the `rules` key of the config, in file order, as `user/<name>`.
3. Your rules from each `--rules` file, in the order of the flags.

No rule matched: the resource is `unmanaged` (rule `rules/no-match`), or stays `undetermined` when
its type has no mapping.

Because packs run first, your rule can't override a pack's verdict for the same resource. To
replace a pack's view, turn the pack off with `scan.packs.<name>: false` and write your own rules.
The [rule packs](/reference/rule-packs) page lists every pack rule and its expression.

Some details of the evaluation:

- Each pack sets one category for all its rules: `aws-defaults` and `aws-service` set
  `aws-managed` with owner `aws`, `other-iac` sets `other-iac`, `derived` sets `derived`, and
  `ephemeral` sets `ignored`.
- A rule whose expression fails on a resource, such as a missing key read without `?`, counts as
  no match for that resource.
- Rule names must be unique. tfcensus compiles every rule when it starts, reports each error with
  the rule's name and column, and exits 2.
- After its `expires` date (UTC, inclusive) a user rule stops matching, and `scan` warns once per
  run that it expired. Use it for temporary exceptions.

## Guards

Some resources are risky enough that you may never want a broad rule to hide them. A guard marks
such a resource. Today there is one guard, `guard/iam-user`: every IAM user is guarded, because a
user can hold long-lived credentials and AWS Config doesn't record whether it has a console
password.

A guard sets no category itself. It acts through `scan.never_hide`, a list of categories. A user
rule whose category is in `never_hide` doesn't apply to a guarded resource, so the resource stays
`unmanaged`. With this config, a rule that marks all `ci-*` resources as `ignored` still leaves
an IAM user named `ci-deploy` in the report:

```yaml
scan:
  never_hide: [ignored]
```

Guards limit user rules only. Pack rules still apply, and `never_hide` also keeps a guarded child
from [inheriting](/concepts/categories#inheritance) a hidden parent's category.

## See the trace

`scan --explain` and `rules test --resource` print every rule's outcome for one resource: match,
no match, error, expired, pack off, never hide, or skipped because an earlier rule matched.

```sh
tfcensus rules test rules.yaml --resource sample.json
```
