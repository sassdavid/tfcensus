## tfcensus completion nushell

Generate the autocompletion script for nushell

### Synopsis

Generate the autocompletion script for nushell. carapace, built into tfcensus, writes it.

The script defines $tfcensus_completer. Save it, source it from config.nu and use it as
the external completer (or call it from yours when the first span is tfcensus):

  tfcensus completion nushell | save --force ~/.config/nushell/tfcensus.nu

  # config.nu
  source ~/.config/nushell/tfcensus.nu
  $env.config.completions.external = {enable: true, completer: $tfcensus_completer}


```
tfcensus completion nushell [flags]
```

### Options

```
  -h, --help[=true|false]   help for nushell
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

* [tfcensus completion](tfcensus_completion.md)	 - Generate the autocompletion script for the specified shell

