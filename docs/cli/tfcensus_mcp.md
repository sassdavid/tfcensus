## tfcensus mcp

Serve units, inventory, whois, scan_summary, audit_findings and drift_summary to an MCP client over stdio

### Synopsis

mcp runs a Model Context Protocol server on stdin and stdout, for agents such as Claude Code.
Logs go to stderr. Each tool returns the JSON envelope the matching command prints with
--format json:

  units           tfcensus report units --index <file>
  inventory       tfcensus report inventory --index <file>
  whois           tfcensus whois --index <file> <arn|id>... (no --live, no accounts)
  scan_summary    tfcensus report scan [--from <file>]
  audit_findings  tfcensus report audit --from <file>
  drift_summary   tfcensus report drift --from <file>

The tools answer from saved files only. They never call AWS, start terragrunt or terraform,
read a state, run a plan, or write a file, and move apply, rollback and mark-applied are not
tools. Run the commands that collect (inventory --save-index, scan, audit, drift) yourself,
and pass their output with the flags below. A tool whose file wasn't given fails with a
message naming the flag. scan_summary without --scan reads the newest scan cache snapshot and
applies the config's ownership rules; a snapshot older than cache.ttl comes with a warning.
The files are read again on every call, so a new run is picked up without a restart.

A result over --max-result-bytes (default 50,000, Claude Code's limit for showing a result
to the model) is split. The envelope then has a page field, and the agent passes
page.next_cursor back to the same tool for the rest. Double-managed and unmanaged resources
and high-severity findings come first. A cursor lasts 15 minutes after its last use.

```
tfcensus mcp [flags]
```

### Examples

```
  tfcensus inventory --save-index ~/.cache/tfcensus/index.json.zst
  claude mcp add --transport stdio --scope local tfcensus -- tfcensus mcp --index ~/.cache/tfcensus/index.json.zst
  tfcensus mcp --index index.json.zst --scan scan.json --audit audit.json --drift drift.json
```

### Options

```
      --audit string           audit JSON for audit_findings
      --drift string           drift JSON for drift_summary
  -h, --help[=true|false]      help for mcp
      --index string           state index from tfcensus inventory --save-index, for units, inventory and whois
      --max-result-bytes int   split results larger than this many bytes of JSON into pages; 0 never splits (default 50000)
      --scan string            scan JSON for scan_summary (default: the newest scan cache snapshot)
```

### Options inherited from parent commands

```
      --color string            colorize output: auto, always or never (default "auto")
      --config string           config file to use instead of the discovered .tfcensus.yaml (env TFCENSUS_CONFIG)
      --format string           output format: tty, plain, md, json, csv, sarif or github (default tty when stdout is a terminal, else plain)
      --log-file string         write logs to this file instead of stderr
      --log-format string       log format: text or json (default "text")
      --no-input[=true|false]   never prompt for input
  -q, --quiet[=true|false]      log errors only
      --tf-path string          terraform or tofu binary, passed to terragrunt as --tf-path (config binaries.terraform) (default "terraform")
      --tg-path string          terragrunt binary (config binaries.terragrunt) (default "terragrunt")
  -v, --verbose count           more logging: -v info, -vv debug, -vvv trace
```

### SEE ALSO

* [tfcensus](tfcensus.md)	 - Take a census of Terraform/OpenTofu and Terragrunt fleets on AWS

