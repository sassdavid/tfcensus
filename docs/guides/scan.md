---
description: "Find the AWS resources no Terraform or OpenTofu state manages, find out why one landed in its category, and fail a CI job on unmanaged or double-managed resources."
---

# Find unmanaged resources and gate CI with --fail-on

`tfcensus scan` lists the live resources of your accounts, matches them against the state of every
unit it discovers, and puts each resource in a [category](/concepts/categories). This guide runs a
scan, follows one finding back to its cause, and turns the scan into a CI gate.

## Run a scan

Point `scan` at the repositories that hold your units and at the accounts they deploy to:

```sh
tfcensus scan ~/src/infra/acme-live --profile acme-nonprod --region eu-west-1
```

Without paths, `scan` discovers units under `discovery.roots`, else the working directory. Without
`--region`, it lists the regions that the units in each account target, so an account that no unit
targets needs `--region` or `--all-regions`. The [accounts guide](/guides/accounts) covers
`--profile`, `--org` and the include and exclude lists.

The output starts with one row per account and region, then lists the findings by category:

<<< @/snippets/scan.txt

Coverage is managed and double-managed resources divided by those plus the unmanaged ones.
Resources in the other categories don't count either way. The warnings at the end name rules past
their `expires` date and resources that a state and a CloudFormation stack both list.

## Narrow the scan

- `--types` takes CloudFormation or Terraform type globs, such as `'AWS::IAM::*'` or
  `'aws_security_group*'`. It is repeatable.
- `--account` and `--exclude-account` keep or drop accounts. `scan` still reads the state of units
  in a dropped account, so a resource managed from there still counts as managed.
- `--filter` passes a terragrunt filter expression to `terragrunt find`, to select units.
- `--layers` picks inventory layers. The default, `auto`, uses every layer `doctor` found.

A scan of many accounts is faster with an AWS Config aggregator and Resource Explorer views; see
[Speed up scans](/guides/aggregator).

## Find out why a resource is in its category

`--explain` takes one ARN or ID and prints how `scan` decided its category: the layers that listed
it, the match against the states, every ownership rule it tried and the parent it inherited from:

```sh
tfcensus scan --explain arn:aws:iam::111111111111:role/clickops-admin
```

The trace for a policy of a service-linked role ends like this. The resource matched no rule
itself, so it took the category of its parent role (rule `inherit/aws-managed`):

```text
parent    AWS::IAM::Role arn:aws:iam::111111111111:role/aws-service-role/ecs.amazonaws.com/AWSServiceRoleForECS, aws-managed (pack/aws-service/service-linked-role)
category  aws-managed, owner aws: child of AWS::IAM::Role arn:aws:iam::111111111111:role/aws-service-role/ecs.amazonaws.com/AWSServiceRoleForECS, aws-managed (pack/aws-service/service-linked-role) (inherit/aws-managed)
```

With `--format json`, `.data.resource` is the resource as rules see it. Save it and give it to
`tfcensus rules test --resource` to try a new rule against it offline, as the
[rules guide](/guides/rules) shows.

`--explain-coverage` answers a different question: which layer listed each type in each account and
region, which records a layer left out, and which AWS Config types tfcensus doesn't map. A type
with no mapping is `undetermined`, never managed. The [type coverage](/reference/type-coverage)
page lists every mapping.

## Hide what you accept

The built-in [rule packs](/reference/rule-packs) already sort out default VPCs, service-linked
roles, resources that CloudFormation, Pulumi or Kubernetes controllers own, and AWS-created
children. What stays unmanaged and is fine, such as a vendor's integration role, gets a rule of
your own with a reason and an optional `expires` date. [Write and test ownership rules](/guides/rules)
shows how.

Two scan settings change what the packs see:

- `scan.cloudformation` (on by default) lists the resources of every CloudFormation stack. A
  resource a stack lists and no state manages is `other-iac` with rule `source/cloudformation`, and
  the finding names the stack. Turn it off to save the `cloudformation:ListStacks` and
  `ListStackResources` calls.
- `scan.external_dns` sets the TXT registry naming of your external-dns deployments, so the
  `external-dns` rule finds the records they own. Set `txt_prefix` and `txt_suffix` to the values
  of external-dns's `--txt-prefix` and `--txt-suffix`; either may hold `%{record_type}`. When both
  are empty, tfcensus tries no prefix, then `external-dns-`.

```yaml
scan:
  external_dns:
    txt_prefix: "%{record_type}-extdns-"
```

## Write reports for CI

`--format` sets the format on stdout, and `--out format=path` writes more files from the same run.
`--out` is repeatable:

```sh
tfcensus scan --profile acme-nonprod --out json=reports/scan.json --out sarif=reports/scan.sarif --out md=reports/scan.md
```

The JSON follows the [scan schema](/reference/schemas). SARIF goes to GitHub code scanning, and
`github` writes workflow annotations. Markdown is cut at a row or section boundary once it reaches
`--md-max-chars` (60,000 characters), under GitHub's comment limit, with a footer that says what
was cut. [Output formats](/reference/output) describes each format.

## Fail the job

`--fail-on` makes `scan` exit 3 when it finds resources in the categories you name: `unmanaged`,
`double-managed`, or both. `--min-count` sets how many findings it takes:

```sh
tfcensus scan --profile acme-prod --types 'AWS::IAM::*' --fail-on unmanaged --fail-on double-managed --min-count 1
```

The report is still written before the exit, so a CI step can upload it. The other exit codes tell
a gate apart from a broken run:

| Code | Meaning |
|---|---|
| 0 | The scan finished, and nothing matched `--fail-on`. |
| 1 | A layer, a state read, a stack listing or discovery failed. |
| 3 | `--fail-on` matched at least `--min-count` findings. |
| 4 | Every failure was a credential problem. |

Every code is on the [exit codes](/reference/exit-codes) page. To publish the report as a Slack
message, an issue or a pull request comment, pass the JSON to `tfcensus notify`
([Publish reports](/guides/notify)). `tfcensus report html` turns it into a single HTML file
([HTML report](/guides/html-report)).

## Run it again without AWS calls

`scan` caches the inventory and the state index for `cache.ttl`, one hour by default, keyed by the
accounts, regions, layers, types, units, `scan.cloudformation` and whether state decryption is on.
A second run with other rules or another format reads the cache and makes no AWS calls.
`--refresh` lists again. In CI, `tfcensus inventory --save-index` writes the state index once, and
`scan --index` reads it in place of the states. [Caches](/concepts/caches) explains what is kept.
