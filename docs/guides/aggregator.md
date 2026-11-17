---
description: "Make scan faster across many accounts: read AWS Config through an organization aggregator, turn on Resource Explorer views, and narrow layers, types and regions."
---

# Speed up scans with the Config aggregator and Resource Explorer

`scan` lists every selected account and region through its [inventory layers](/concepts/inventory-layers).
Without help from AWS, that means AWS Config queries in each account and region, and Cloud Control
calls for the types nothing else lists. Cloud Control is slow and throttled. Two AWS features that
an admin turns on make a scan faster: an AWS Config aggregator and Resource Explorer views.
tfcensus never turns either on. It only reads them.

## See what each account has

`doctor` probes each account and region and shows the layers a scan can use there:

```sh
tfcensus doctor --profile 'acme-*'
```

<<< @/snippets/doctor.txt

The `LAYERS` column is what `scan --layers auto` uses. A region with `re, config, cc, native` has
both fast layers. A region with only `cc, native` lists most types through Cloud Control. The notes
under the table say what an admin could turn on. Probe results are cached for 24 hours, and
`--refresh` probes again.

## Read AWS Config through an aggregator

An organization usually runs an AWS Config aggregator in a delegated admin or audit account. Name
it in `accounts.aggregator`, and the config layer reads every account and region the aggregator
serves with `SelectAggregateResourceConfig` in the aggregator's account, in place of one query per
account and region:

```yaml
accounts:
  aggregator:
    name: acme-org
    profile: acme-audit
    region: eu-central-1
```

`profile` is the profile for the aggregator's account, and the default credentials when it is
empty. `region` is the aggregator's region, and the profile's own region when it is empty. An
empty `name` turns the aggregator off. The keys also have environment variables, such as
`TFCENSUS_ACCOUNTS_AGGREGATOR_NAME` ([configuration](/reference/config#accounts-aggregator-name)).

`doctor` then prints a line for the aggregator, with its sources and how many of them failed or
are outdated. `scan` and `whois` use the aggregator for an account and region only when it holds
current data there: a source that covers the pair succeeded, and none failed or is outdated. An
organization source covers every account in its region. Pairs the aggregator doesn't serve, and
pairs it returned nothing for, are queried in the account as before. The config layer still runs
only where the account's own recorder is recording.

The scan report's JSON has an `aggregator` object with the rows read, the account and region pairs
served and the pairs that fell back to per-account queries. The aggregator calls are
`config:DescribeConfigurationAggregators`, `config:DescribeConfigurationAggregatorSourcesStatus`
and `config:SelectAggregateResourceConfig`, which the [read-only policy](/reference/iam) grants.

## Turn on Resource Explorer

The `re` layer lists a region through Resource Explorer's default view. It runs in each region
that has an index and a default view, and it covers types that AWS Config doesn't record. tfcensus
never creates an index or a view, and it never picks one of several views. Without a default view,
the layer doesn't run in that region.

An admin turns it on with `aws resource-explorer-2 create-index` in each region, then
`aws resource-explorer-2 create-view` and `associate-default-view`. The `doctor` notes name the
regions that miss one or the other.

A Resource Explorer aggregator index doesn't make the `re` layer faster. The layer searches each
region in that region's own index. AWS copies the other regions into an aggregator index in the
background, and that can take several hours, so the local index is the current one.

## Use fewer calls

When AWS Config and Resource Explorer are on, a few more settings cut the work:

- `--types` limits the scan to some types, as CloudFormation or Terraform globs:
  `tfcensus scan --types 'AWS::IAM::*'`.
- `--region` limits the regions. By default `scan` lists the regions that the units in each
  account target, and `--all-regions` lists every enabled region.
- `--layers` (`scan.layers`) picks layers by name, such as `config,re,native`, to leave Cloud
  Control out. Types that only Cloud Control lists are then not listed at all, and the
  [type coverage](/reference/type-coverage) page says which those are.
- `scan.cloudcontrol_types` adds CloudFormation types to the `cc` layer. Keep the list short:
  Cloud Control calls are slow.
- `--index` reads the state index from a file that `tfcensus inventory --save-index` wrote, so the
  scan reads no state.

`scan` caches the inventory and the state index for `cache.ttl` (one hour by default). A second
run with other rules or another output format makes no AWS calls, and `--refresh` lists again. The
[caches page](/concepts/caches) has the details.

## Check where each type came from

`tfcensus scan --explain-coverage` prints which layer listed each type in each account and
region, the records a layer left out, and the AWS Config types that tfcensus doesn't map, with
their counts. Run it after a change to see that the aggregator or a new view took over:

```sh
tfcensus scan --profile 'acme-*' --explain-coverage
```
