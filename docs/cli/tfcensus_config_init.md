## tfcensus config init

Write a commented config file

### Synopsis

Writes a config file that lists every key commented out at its default, so the file
changes nothing until you uncomment a key. It goes to .tfcensus.yaml in the working
directory, to path, or with --user to the user file. The directory is created when it's missing.

An existing file is kept, with exit code 2, unless you pass --force. The written path goes
to stderr.

```
tfcensus config init [path] [flags]
```

### Examples

```
  tfcensus config init
  tfcensus config init --user
  tfcensus config init ci/tfcensus.yaml --force
```

### Options

```
      --force[=true|false]   replace an existing file
  -h, --help[=true|false]    help for init
      --user[=true|false]    write the user file instead of .tfcensus.yaml
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
      --tofu-path string        tofu binary that --decrypt-with-tofu runs (config binaries.tofu) (default "tofu")
  -v, --verbose count           more logging: -v info, -vv debug, -vvv trace
```

### SEE ALSO

* [tfcensus config](tfcensus_config.md)	 - Inspect and validate configuration

