---
description: How tfcensus reads Terraform and OpenTofu state from S3 backends, with which credentials, without a lock, and what each read status means.
---

# How state is read

`inventory`, `scan`, `audit backend` and `whois` read the state of every unit they find. tfcensus
reads the state objects from S3 itself. It runs no terraform or terragrunt for this, takes no lock
and writes nothing back. This page covers what it reads, with which credentials, and what can go
wrong.

## S3 backends only

tfcensus reads state from the `s3` backend. A unit with another backend, such as `local`, `remote`
or a `cloud` block, is listed with the status `skipped` and the warning
`backend <type>: tfcensus reads state from s3 only`. A unit whose backend
[discovery](/concepts/units) couldn't resolve is `skipped` too, with the reason.

## Credentials per backend

Each state is read with the credentials that reach its bucket, which can differ from the account
the unit manages:

1. When the backend names a `profile`, tfcensus loads that profile.
2. Otherwise it finds the profile that owns the bucket's account: `accounts.map` first, then the
   accounts that `--profile` and `--org` selected, then the default AWS credentials. The bucket's
   account is the account of the backend's `role_arn`, or the unit's target account when the
   backend has no role.
3. When the backend sets `role_arn`, tfcensus assumes that role from the profile it found.

Reading one state takes `s3:GetObject`, and `kms:Decrypt` when the bucket uses SSE-KMS. Listing
workspaces takes `s3:ListBucket`. The [read-only policy](/reference/iam) has all three. An
access-denied error names the action and the S3 location, so you can tell which grant is missing:

```text
tf-fleet/restricted: error: access denied: s3:GetObject on s3://acme-restricted-tfstate/restricted/terraform.tfstate; the backend credentials need s3:GetObject, and kms:Decrypt when the bucket uses SSE-KMS
```

The [accounts guide](/guides/accounts) shows how to select profiles and map accounts.

## No lock

tfcensus reads the state object with `s3:GetObject` and never touches the lock file or the
DynamoDB lock table. A read never blocks a `terraform apply`, and an apply in progress doesn't
block the read. When an apply writes the state during a run, the report shows the version that
was current when tfcensus read it.

## Workspaces

The `s3` backend keeps the default workspace at the backend `key`, and every other workspace at
`<workspace_key_prefix>/<workspace>/<key>`, with `env:` as the default prefix. tfcensus lists that
prefix with `s3:ListBucket` and reads every workspace it finds, each as its own row. When the
listing is denied, the default workspace is still read, and the unit gets the warning
`workspaces not listed`.

## What is kept

tfcensus reads state files of format version 4, which terraform 0.12 and later and every OpenTofu
release write. The parser keeps resource addresses, types, provider addresses, IDs, ARNs and
identity attributes, and drops every other attribute value. Security group rules are kept only as
hashes, for matching. No output, log, cache or saved index holds an attribute value
([safety model](/concepts/safety)).

Raw state names the providers a unit uses but not their versions. tfcensus reads the versions from
the unit's `.terraform.lock.hcl` when it has one.

`inventory` also says which tool applied a state when something shows it: an encrypted state or a
provider from `registry.opentofu.org` means OpenTofu, then a `.opentofu-version` or
`.terraform-version` file from the unit up to the repo root, then a `tofu` binary in `--tf-path`
for a Terragrunt unit.

## Read statuses

Each row of `inventory` has one status:

| Status | Meaning |
|---|---|
| `ok` | the state was read |
| `absent` | the state object doesn't exist: the unit was never applied |
| `encrypted` | OpenTofu client-side encryption; only serial and lineage are readable ([read encrypted state](/guides/encrypted-state)) |
| `unsupported` | a state format older than version 4, written by terraform before 0.12 |
| `error` | the read failed, for example access denied; the reason is under the table |
| `skipped` | no `s3` backend to read |

A failed read is reported for its unit and the run goes on with the others. `inventory` then exits
1, or 4 when every read failed on credentials ([exit codes](/reference/exit-codes)).

<<< @/../testdata/golden/inventory/tf-fleet.txt

States are read 16 at a time. `inventory --concurrency` changes that.

## The identifier index

`inventory --save-index <file>` writes every managed resource's identifiers to a file: unit,
workspace, address, type, provider, ID, ARN, identity, account and region. `whois --index`, `scan --index` and
`mcp --index` read it later without AWS credentials. A name ending in `.zst` is compressed with
zstd, and `.gz` with gzip. The [whois guide](/guides/whois) shows how to use it offline.

## Feed relgap

relgap is a sibling tool that reports which tool versions your repositories pin. Its `drift`
command finds version drift across repos, and with `--from-tfcensus` it takes the list of roots
from the JSON that `tfcensus inventory --format json` prints:

```sh
tfcensus inventory ~/src/infra/acme-live --format json > inventory.json
```

relgap's [guide to `drift --from-tfcensus`](https://relgap.sasskovacs.dev/guides/from-tfcensus)
shows the rest.
