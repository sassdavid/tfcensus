# Dogfood: tfcensus mcp in Claude Code

On 2026-11-12 the author registered `tfcensus mcp` in Claude Code and asked it a scan question and
a whois question about one real AWS account, read-only through the `collection-x-product` profile.
This record holds aggregates only: no account IDs, ARNs, resource names, unit paths or bucket names.

## Data

The repository is one environment directory of a Terragrunt live repository with 43 units, all
with S3 backends in the same account.

```sh
tfcensus units "$REPO" --format json > units.json
tfcensus inventory "$REPO" --profile collection-x-product --save-index index.json.zst --format json > inventory.json
tfcensus scan "$REPO" --profile collection-x-product --index index.json.zst --format json > scan.json
```

| Step | Result |
|---|---|
| units | 43 units, 43 with an S3 backend and a known target account |
| inventory | 43 states read, all `ok`; 0 double-managed resources; no errors |
| scan | 1 account, 10,390 resources, 41 s; every layer `ok` (config, resource-explorer and 4 native layers); no errors |

Scan categories: 1,038 managed, 0 double-managed, 5,047 unmanaged, 275 other-iac, 100 aws-managed,
320 derived and 3,610 undetermined. Coverage is 17.1%. The low coverage is expected: the 43 units
are one environment of an account that holds several.

## Registration

The binary and the data stay inside the tfcensus checkout (`./tfcensus` and `dist/dogfood/`, both
ignored by git). The server is registered in Claude Code's local scope, so only this project
sees it:

```sh
go build -o ./tfcensus ./cmd/tfcensus
claude mcp add --transport stdio --scope local tfcensus -- \
  "$PWD/tfcensus" mcp --index "$PWD/dist/dogfood/index.json.zst" --scan "$PWD/dist/dogfood/scan.json"
claude mcp get tfcensus   # Scope: Local config, Status: Connected
```

## Questions

Both questions ran headless in the tfcensus checkout, with only the tfcensus tools allowed:
`claude -p "<question>" --allowedTools mcp__tfcensus__scan_summary,mcp__tfcensus__whois`.

The first question asked for the number of unmanaged resources, the coverage and the three most
common unmanaged types. It also asked whois about two ARNs from the scan, one managed and one
unmanaged. Answers, in 4 turns:

- 5,047 unmanaged resources and 17.06% coverage, which match `scan.json`.
- The first ARN is managed by 1 unit, matched by identity (`match/identity`).
- The second ARN is unmanaged: no state manages it and no rule matches it (`rules/no-match`).
- The three most common types couldn't be named. The top 50 findings were all one type, because
  `scan_summary` listed findings in the scan's type order and had no per-type counts.

That gap led to a fix in the same milestone. `scan_summary`, and `tfcensus report scan`, now
return `types`: the matching findings per type, most common first. Asked again, the model answered
in 4 turns with 5,047 unmanaged, 17.1% coverage, and the top three types
`AWS::EC2::NetworkInterface` (739), `AWS::IAM::RolePolicy` (603) and `AWS::Logs::LogGroup` (570).
A count over `scan.json` gives the same three types and numbers.

No tool call reached AWS, terragrunt or terraform. The answers came from the two saved files.
