# tfcensus ADR-0009: The scan caches one snapshot per input set, not results per layer

- Status: proposed (2026-11-11)

## Context
The inventory spec plans a results cache per (account, region, layer) for 1 hour. Scan criterion 4
asks that a re-run with different `--rules` make no AWS calls. A per-layer cache can't meet that:
the re-run still calls STS to resolve accounts, the probe for regions, and S3 to read every state.

## Decision
- `scan` caches a snapshot: the merged inventory (with every layer's run and the coverage matrix),
  the state index, and the problems found while collecting them. Ownership rules aren't part of
  it, so they run on every scan.
- The key is a hash of the inputs that change what a scan collects: the tfcensus version, the
  accounts config, the regions, the layers, the `--types` CloudFormation types, the Cloud Control
  allowlist, every discovered unit with its backend and target, and the `--index` file's size and
  modification time. Discovery runs on every scan (no AWS calls), so a new unit is a new key.
- A hit skips account resolution, the probe, state reads and every layer. It is fresh for
  `cache.ttl` (default 1h). `--refresh` collects again.
- Files live under `$XDG_CACHE_HOME/tfcensus/scan/<key>.json.zst` (mode 0600), written atomically
  with the index file writer. They hold identifiers and tags only, like the saved index.

## Consequences
- Re-runs that only change rules, formats, `--fail-on`, `--explain` or `--explain-coverage` are
  instant and make no AWS calls (`TestSpec_scan_4` counts them).
- Changing one account or region collects everything again. A per-layer cache can be added under
  the snapshot later, if large estates need it.
- `cache info|clear` will list and remove these files along with the probe cache.
