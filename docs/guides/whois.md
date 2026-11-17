---
description: "Find which units and state addresses manage an AWS ARN or ID, check a batch from a file or a scan report, and answer offline from a saved state index."
---

# Find who manages an ARN, offline too

`tfcensus whois` takes an ARN or a Terraform resource ID and tells you which units manage it: the
unit, the resource address in its state, the account and region, and the key that matched. More
than one state means the resource is double-managed. When no state manages it, the ownership rules
say why.

## Ask about one resource

Give `whois` an ARN and the repository to discover units in:

```sh
tfcensus whois arn:aws:iam::111111111111:role/acme-app --repo ~/src/infra/acme-live --profile acme-nonprod
```

`--repo` is repeatable, and defaults to `discovery.roots`, then the working directory. `whois`
reads every unit's state, so it needs the same credentials as `inventory`; see
[Choose accounts](/guides/accounts).

The answer for four roles of the fixture fleet looks like this:

<<< @/snippets/whois.txt

`whois` matches with the same keys as `scan`: identity, then ARN, then the normalized ID
([Matching](/concepts/matching)). The word after "by" is the key that matched.

## Look up a bare ID

An ARN whose type tfcensus maps is matched like a live resource. A bare ID, such as a security
group ID, and an ARN of an unmapped type are looked up across every type. `--type` narrows the
lookup to some Terraform types, which avoids a match on an unrelated resource with the same ID:

```sh
tfcensus whois sg-0aa11bb22cc33dd44 --type aws_security_group
```

## Explain an unmanaged resource

When no state manages a resource, the [ownership rules](/concepts/rules) say why: `other-iac`,
`aws-managed`, `ignored` or `unmanaged`, with the rule ID. Without `--index`, `whois` also lists
the resource's type in its account and region through the scan inventory layers, so the rules see
its tags and relations. The answer then ends with `live: found`, or with `live: not-found` and the
category `undetermined` when the resource is gone. A resource that a CloudFormation stack created
shows as `other-iac` this way, as the last answer in the output above.

An account that `--account` or `--exclude-account` leaves out is never listed live, so its answers
stay offline. `--rules` adds rule files, as in `scan`.

## Check many resources at once

`--stdin` reads more ARNs or IDs from stdin, one per line. Blank lines and lines that start with
`#` are skipped, and duplicates are asked once. A scan report is a good source:

```sh
jq -r '.data.findings[].arn // empty' reports/scan.json | tfcensus whois --stdin --repo ~/src/infra/acme-live
tfcensus whois --stdin < arns.txt
```

The answers can go out as text, Markdown, JSON or SARIF, with `--format` and the repeatable
`--out format=path`. The JSON follows the [whois schema](/reference/schemas).

## Answer offline from a saved index

Reading every state takes time and credentials for every backend. `tfcensus inventory --save-index`
writes the identifier index once: the identifiers, addresses and units of every state, and no
attribute values. A `.zst` name compresses it with zstd and `.gz` with gzip:

```sh
tfcensus inventory ~/src/infra/acme-live --profile 'acme-*' --save-index index.json.zst
tfcensus whois --index index.json.zst arn:aws:iam::111111111111:role/acme-app
```

With `--index`, `whois` reads no state and makes no AWS calls. The rules see only what the ARN
gives, so the answer says `tags not read (offline)`. `--live` lists the resource's type anyway,
with credentials for its account. `--index` and `--repo` don't go together.

A CI job can save the index as an artifact, and anyone can then answer questions from it without
access to the state buckets. `scan --index` reads the same file. The MCP server's `whois` tool
answers from it too ([MCP guide](/guides/mcp)).

## Gate on double-managed resources

`--fail-on` makes `whois` exit 3 when answers fall in `unmanaged` or `double-managed`, and
`--min-count` sets how many it takes:

```sh
tfcensus whois --index index.json.zst --fail-on double-managed arn:aws:s3:::acme-assets
```

The exit code is 1 when a state read or a layer failed, and 4 when every failure was a credential
problem. Rules past their `expires` date print a warning, as in `scan`.
