## tfcensus completion fish

Generate the autocompletion script for fish

### Synopsis

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	tfcensus completion fish | source

To load completions for every new session, execute once:

	tfcensus completion fish > ~/.config/fish/completions/tfcensus.fish

You will need to start a new shell for this setup to take effect.


```
tfcensus completion fish [flags]
```

### Options

```
  -h, --help[=true|false]              help for fish
      --no-descriptions[=true|false]   disable completion descriptions
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

