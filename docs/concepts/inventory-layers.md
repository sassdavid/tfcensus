---
description: The sources a scan lists live AWS resources from, which one wins when two report the same resource, and how to see the blind spots with --explain-coverage.
---

# Inventory layers and coverage

`scan` needs a list of what exists in AWS before it can say what no state manages. It builds that
list from inventory layers: AWS Config, Resource Explorer, Cloud Control and tfcensus's own
listers, the `native:*` layers. Each layer covers some resource types, in some accounts and
regions. This page says what each layer lists, how their results merge, and how to find the types
no layer covered.

## The layers

<!--@include: ../parts/inventory-layers.md-->

A regional layer runs once per account and region. A layer that runs once per account lists
account-wide resources, such as IAM roles and Route 53 records, and reports them in the `global`
column.

## Which layers run

`--layers auto`, the default, runs every layer the capability probe allows in each account and
region. `doctor` shows the result of that probe in its `LAYERS` column:

- `config` runs where an AWS Config recorder is recording. With `accounts.aggregator` set, one
  aggregator query serves every account and region whose aggregator sources are current
  ([aggregator guide](/guides/aggregator)).
- `re` runs where a Resource Explorer index has a default view.
- `cc` and the `native:*` layers run everywhere, because they need no setup in the account.

Cloud Control lists the types that neither AWS Config nor Resource Explorer lists, plus the types
in `scan.cloudcontrol_types`. It is the slowest layer and AWS throttles it, so in auto mode it
skips any of those types that an earlier layer already listed in the same account and region.

`--layers` also takes a comma-separated list of layer names, such as `config,native`. `native`
stands for every `native:*` layer, and a single one is named in full, such as
`native:route53-records`. `scan.layers` sets the same value in the config file. The first scan
runs the native listers only, and the second reads AWS Config as well:

```sh
tfcensus scan --profile acme-nonprod --layers native
tfcensus scan --profile acme-nonprod --layers config,native
```

`native:eks-pod-identity` reads the clusters another layer found, so it runs after the others and
lists nothing when no layer found a cluster.

## How records merge

When two layers report the same resource (the same ARN, or the same type and ID without an ARN),
tfcensus keeps one record. Layers are folded in the order of the table above: an earlier layer's
value wins, and a later layer fills only the fields that are still empty. Tags come from the first
layer that has any. The finding's `sources` lists every layer that reported it.

So with both AWS Config and Resource Explorer on, a resource's attributes come from AWS Config,
and Resource Explorer adds what Config didn't record.

## Coverage and blind spots

A type that no layer lists in a region is a blind spot: a resource of that type isn't in the
report at all, so it can't show as unmanaged. `--explain-coverage` prints, per type, which layer
listed it in each account and region:

```sh
tfcensus scan --profile acme-nonprod --explain-coverage
```

An empty cell is a blind spot. A cell such as `config!` means the layer ran there but was denied or
failed. The output also lists the records a layer left out, and the types AWS Config records that
tfcensus doesn't map to a Terraform type, with counts from `config:GetDiscoveredResourceCounts`.
Those types are `undetermined` in a scan ([categories](/concepts/categories)), and the
[type mapping coverage](/reference/type-coverage) page lists every mapped type.

Coverage in the scan summary is a different number: the share of resources a state manages. The
[categories](/concepts/categories#coverage) page explains it.

## Failed layers

A layer that is denied or fails in one account and region doesn't stop the scan. The report lists
it in its notes, such as `layer config in 111111111111 eu-west-1: denied`, and the scan exits 1
(4 when every failure was a credential problem). The types that layer would have listed there are
blind spots for that run. The [read-only policy](/reference/iam) grants every call the layers make.
