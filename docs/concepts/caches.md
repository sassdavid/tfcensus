---
description: The probe and scan caches tfcensus keeps, what they hold, how long they stay fresh, which commands read them, and how to inspect or clear them.
---

# Caches

tfcensus keeps two caches, so that a second `doctor` or `scan` doesn't call AWS for answers it
already has. Both live under `$XDG_CACHE_HOME/tfcensus`, which is `~/.cache/tfcensus` when
`XDG_CACHE_HOME` isn't set. `tfcensus cache info` prints the directory.

| Cache | Holds | Written by | Fresh for |
|---|---|---|---|
| `probe` | the capability probe of one account and region: whether Resource Explorer, AWS Config and Cloud Control can serve a scan | `doctor` and `scan` | 24 hours |
| `scan` | one snapshot of a scan's collection: the live resources from every layer and the state index | `scan` | `cache.ttl`, default 1 hour |

Both hold identifiers, tags and probe results. Neither holds a state attribute value, because
tfcensus never keeps one in the first place ([how state is read](/concepts/state)).

## The probe cache

`doctor` and `scan` probe each account and region for the inventory sources they can use, and
write one file per account and region. For 24 hours, both reuse that result instead of probing
again. After an admin turns on Resource Explorer or AWS Config, run `doctor --refresh` or
`scan --refresh` to probe again.

## The scan cache

A scan does two slow things: it lists the live resources of every account and region, and it reads
every state. The scan cache keeps both as one snapshot, keyed by everything that changes what a
scan collects: the tfcensus version, the accounts, regions, layers, types, Cloud Control types, the
units with their backends and targets, `--index`, `scan.cloudformation` and `scan.external_dns`.

Ownership rules aren't part of the key. A scan with the same inputs and other rules makes no AWS
call: it applies the new rules to the cached snapshot. That makes trying a rule cheap: edit it, run
`scan` again, and look at the categories.

`scan --refresh` probes, lists and reads everything again and writes a new snapshot. `cache.ttl` sets how
long a snapshot stays fresh, as a Go duration such as `30m` or `4h`. Its variable is
`TFCENSUS_CACHE_TTL`.

Two commands read the newest snapshot without calling AWS: `report scan` without `--from`, and
the MCP tool `scan_summary` when `tfcensus mcp` runs without `--scan`. Both apply the current
ownership rules, and both use a snapshot older than `cache.ttl` with a warning.

## Inspect and clear

`cache info` prints the cache directory, then one row per cache with its entry count, size in
bytes, how many entries are fresh or stale, and the time to live. It reads only file names and
times, never the cached data:

```sh
tfcensus cache info
```

`cache clear` deletes every file in both caches and prints what it removed. The next `doctor` or
`scan` probes and collects again:

```sh
tfcensus cache clear
```

Clear the caches when you want a clean start for both, for example after a change to the accounts
that the cache key can't see, such as new permissions on a role. To refresh one run only, use
`--refresh` instead.

## Files tfcensus doesn't manage

The identifier index from `inventory --save-index` and the JSON reports you write with `--out` are
your files. tfcensus doesn't expire or clear them. [The identifier index](/concepts/state#the-identifier-index)
says which commands read an index.
