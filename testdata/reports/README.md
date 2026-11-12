# Saved report fixtures

These files are the input of the `report`, `notify` and `mcp` tests.

| File | Source |
|---|---|
| `scan.json` | `golden/scan.json` of `testdata/script/spec_scan_1.txtar` |
| `scan-earlier.json` | `scan.json` dated a week earlier, with lower coverage in account 111111111111 and no findings, for the coverage trend |
| `drift.json` | `golden/drift.json` of `testdata/script/spec_drift_1.txtar` |
| `drift-values.json` | `tfcensus drift --show-attributes --format json` over `testdata/drift/ingest`, so it holds attribute values that no output may show |

`TestReportFixtures` fails when `scan.json`, `drift.json` or `drift-values.json` no longer match
their source. Refresh them with `mise run test:golden -- -update`. `scan-earlier.json` is written
by hand.
