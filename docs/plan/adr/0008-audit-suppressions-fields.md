# tfcensus ADR-0008: Audit suppressions match fields with globs until the CEL engine exists

- Status: accepted (2026-11-11)

## Context
ADR-0005 says the CEL rule engine also powers audit suppressions. CEL arrives with the ownership
rules in M5, and the backend audit ships in M4. The backend audit spec shows suppressions as
`{check, bucket, reason, expires}`. That form can't name a lock table, a unit or a state key, so a
lock or fleet finding could only be suppressed for a whole bucket.

## Decision
- An `audit.suppress` entry has `check`, `bucket`, `unit` and `key`, each a glob (`*` and `?`,
  slashes included), plus `reason` (required) and `expires` (optional). An empty field matches
  everything; an entry needs at least one non-empty match field. `key` also matches a finding's
  lock table name.
- A finding is suppressed by the first entry whose set fields all match. The suppressed finding
  stays in the report, with the entry's reason and expiry, and in SARIF as an external suppression.
- Expiry works like ownership rules: `expires` is the last day the entry applies (UTC). After it
  the entry stops matching and the run warns once, with the number of findings it no longer hides.
- `expires` is a quoted `"YYYY-MM-DD"` string. The shared YAML loader turns an unquoted date
  into a timestamp that the config struct can't hold; the schema pattern reports it with file and
  line.
- When CEL lands, an optional `match` expression joins these fields. Existing entries keep
  working.

## Consequences
- Suppressions are less expressive than CEL until M5, and they are enough for the spec's checks.
- Two matching mechanisms exist for a while. The field form stays as shorthand afterwards.
