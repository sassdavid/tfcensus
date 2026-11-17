---
description: "Write an ownership rule for resources that are unmanaged on purpose, test it offline with rules test, and keep it honest with reasons, expiry dates, guards and never_hide."
---

# Write and test ownership rules

Some resources are unmanaged on purpose: a vendor's integration role, an experiment with an end
date, a bucket another team owns. An ownership rule gives them a category, an owner and a reason,
so the next scan stops reporting them as unmanaged. This guide writes a rule, tests it without
AWS, and covers the settings that stop a rule from hiding too much. The
[rules concept page](/concepts/rules) explains the order in which packs and rules run.

## Write a rule

Rules live under `rules` in `.tfcensus.yaml`, or in a separate YAML file with the same shape that
you pass to `scan --rules`:

```yaml
rules:
  - name: datadog-integration
    category: other-iac
    owner: datadog
    match: 'resource.type == "AWS::IAM::Role" && resource.name.startsWith("DatadogIntegrationRole")'
    reason: created by the Datadog CloudFormation quick start
  - name: growth-trial
    category: ignored
    owner: growth
    match: 'resource.type == "AWS::S3::Bucket" && resource.name.startsWith("acme-growth-")'
    reason: trial buckets, deleted when the trial ends
    expires: "2027-03-31"
```

Each rule has these fields:

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Letters, digits, dots, dashes and underscores. Findings show the rule as `user/<name>`. |
| `category` | yes | `other-iac`, `aws-managed`, `derived` or `ignored`. |
| `match` | yes | A [CEL](/reference/cel) expression over `resource` that returns a boolean. |
| `reason` | yes | Why the resource isn't unmanaged. Every finding the rule sets carries it. |
| `owner` | no | Who owns the resource, such as `datadog`. |
| `expires` | no | The last day the rule applies, as a quoted `"YYYY-MM-DD"` date in UTC. |

Pick the category that says who is responsible: `other-iac` for another tool, `aws-managed` for AWS,
`derived` for a child that another resource creates, and `ignored` for anything you decided not to
manage. [Scan categories](/concepts/categories) describes each one.

Rules see the resources that no state manages, including the `undetermined` ones whose type has
no mapping. A rule can't turn a managed or double-managed resource into anything else.

## Write the match expression

`resource` has the account, region, type, ARN, ID, name, tags, attributes, the layers that listed
it, and its parents and relationships. The [CEL reference](/reference/cel) lists every field. A few
patterns cover most rules:

```text
resource.type == "AWS::IAM::Role" && resource.name.startsWith("DatadogIntegrationRole")
resource.tags.?team.orValue("") == "data-platform"
resource.account == "222222222222" && resource.region == "us-east-1"
resource.parents.exists(p, p.type == "AWS::EKS::Cluster")
in_state("aws_lambda_function", resource.name)
```

Read a tag or attribute with `.?key.orValue("")`. Reading a missing key with `resource.tags["team"]`
is an evaluation error, and the rule doesn't match. `in_state(tf_type, id)` is true when a state in
the run manages a resource of that Terraform type with that ID, which helps for a resource whose
parent Terraform manages.

## Get a sample resource

The fastest way to a correct rule is the resource as the rule will see it. `scan --explain` prints
it, and `--format json` gives it in the shape that `rules test` reads:

```sh
tfcensus scan --explain arn:aws:iam::111111111111:role/DatadogIntegrationRole --format json | jq .data.resource > sample.json
```

The explanation in text format also lists every rule the resource went through and the outcome of
each, so you can see which pack or rule decided its category today.

## Test the rules offline

`rules test` compiles the built-in packs, your config's rules and a rules file. Without
`--resource`, it reports the first error with the rule name and column, or how many rules
compiled:

```sh
tfcensus rules test rules.yaml
```

With `--resource`, it evaluates every rule against each resource in a JSON file (one object or an
array) and prints each rule's outcome and the first match:

```sh
tfcensus rules test rules.yaml --resource sample.json
tfcensus rules test rules.yaml --resource sample.json --format json
```

`rules test` makes no AWS calls and reads no state, so `in_state()` is always false there. The
file's rules run after the config's rules, as they do in `scan`. An expired rule shows as expired.

Then run the scan again. The scan cache keeps the inventory for `cache.ttl`, so a re-run with new
rules makes no AWS calls:

```sh
tfcensus scan --profile acme-nonprod --rules rules.yaml
```

## Let rules expire

A rule with `expires` stops matching after that day. `scan` and `whois` then warn about it, and
the resources it hid show up again with their real category:

```text
warning: rule "growth-trial" is past its expires date and no longer matches
```

Use an expiry date for anything temporary: a trial, a migration, a resource waiting for import.
The warning comes back on every run until you delete or extend the rule.

## Turn packs off

The [built-in packs](/reference/rule-packs) run before your rules, and the first match wins. Each
pack has a switch under `scan.packs`, and all of them are on by default:

```yaml
scan:
  packs:
    ephemeral: false
```

A pack that is off shows as `pack off` in the `--explain` trace.

## Guards and never_hide

Some resources are risky to hide. The `guards` pack marks them, and today it marks every IAM user,
because a user can hold long-lived credentials and AWS Config doesn't record whether it has a
console password. A guard sets no category of its own.

`scan.never_hide` lists the categories that a user rule can't give to a guarded resource. With this
config, a rule that would hide an IAM user as `ignored` doesn't apply, the user stays unmanaged, and
the `--explain` trace shows the rule as `never hide`:

```yaml
scan:
  never_hide: [ignored, other-iac]
```

`never_hide` is empty by default. It limits your rules only: the built-in packs still apply to
guarded resources.

## Share rules across teams

`--rules` is repeatable, so a team can keep its own file next to the shared config. The order is
the packs, the config's `rules`, then each `--rules` file in the order given. The first match wins,
so put narrow rules before broad ones.
