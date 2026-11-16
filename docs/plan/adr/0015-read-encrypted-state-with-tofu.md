# tfcensus ADR-0015: Read OpenTofu encrypted state through tofu show -json

- Status: accepted (2026-11-16). Amends [ADR-0002](0002-raw-state-no-provider-binaries.md) for
  encrypted state only. Mitigates risk R6.

## Context
- OpenTofu 1.7 and later can encrypt state on the client. The S3 object then holds `serial`,
  `lineage`, `meta`, `encrypted_data` and `encryption_version`, and the resources are in
  `encrypted_data`. Until now tfcensus reported such a state as `encrypted` with its serial and
  lineage only, so its resources were missing from the index and every scan counted them as
  unmanaged.
- The decryption code (key providers such as pbkdf2, aws_kms and openbao, and the aes_gcm method)
  is in OpenTofu's `internal/encryption`, which tfcensus can't import. The key configuration lives
  in the unit's `encryption` block or in `TF_ENCRYPTION`, and can name other keys and fallbacks.
- `tofu show -json` decrypts the state with that configuration and prints it in the JSON output
  format. It takes no state lock (`internal/command/show.go`). It loads provider schemas to print
  the attributes, so it needs `tofu init` first, which downloads the providers, and then runs them.
  ADR-0002 says tfcensus never runs provider binaries for scanning.

## Decision
- tfcensus detects encryption as before, from the top-level `encrypted_data` or
  `encryption_version` key of the S3 object. tofu runs only for those states.
- Decryption is opt-in: `--decrypt-with-tofu` on inventory, scan, whois, audit backend and doctor,
  or `state.decrypt_with_tofu` in the config. Off, an encrypted state stays `encrypted` and its
  warning names the flag.
- The binary is `binaries.tofu` (`--tofu-path`, default `tofu`), apart from `binaries.terraform`,
  so a fleet that applies most units with terraform can still decrypt its tofu units. doctor
  checks that binary when decryption is on.
- A plain root module runs `tofu init -input=false -no-color -lock=false` and
  `tofu show -json -no-color` in its directory. A terragrunt unit runs the same through
  `terragrunt run --tf-path <tofu> --`, with `TG_TF_PATH`. Each workspace gets its own temporary
  `TF_DATA_DIR` and `TG_DOWNLOAD_DIR`, a non-default workspace gets `TF_WORKSPACE`, and the unit's
  `.terraform.lock.hcl` is put back afterwards. Neither command writes state, S3 or DynamoDB.
- tfcensus never reads, stores or passes keys. tofu gets the `execx` environment allowlist
  (`TF_*`, `TOFU_*`, `AWS_*` and the rest), so `TF_ENCRYPTION` and the AWS credentials an aws_kms
  key provider needs pass through, and the backend reads S3 with the unit's own configuration.
- `tfstate.ParseShow` streams the show output and keeps what the raw parser keeps: id, arn,
  region, identity, identity attributes, rule hashes and the names of sensitive attributes that
  hold a value. Outputs and every other value are skipped unread. Serial and lineage come from the
  encrypted envelope.
- A decrypted state is `status: ok` with `encrypted: true`. A failure keeps `status: encrypted`,
  sets `error` and makes the command exit 1. The error is a fixed message per step (tofu missing,
  init failed, show failed with the key hint, timeout, unreadable output). It never includes
  tofu's stdout or stderr, because both can hold attribute values.

## Consequences
- An encrypted unit's resources reach the index, so scan and whois see them.
- A run with the flag downloads the providers of every encrypted unit and runs them, which needs
  network access to the registry and takes as long as `tofu init`. Runs without the flag behave
  as before, and scanning still works without any binary installed.
- A unit whose key configuration isn't available on the machine fails with exit 1 instead of a
  warning, because the user asked for decryption.
- `scripts/record-tofu.sh` records the tests' tofu output (an encrypted state, its show output and
  the wrong-key error) with a pbkdf2 key in a local backend, and tests replay it with fakebin.
