---
description: Read OpenTofu states that use client-side encryption with --decrypt-with-tofu, which runs tofu init and tofu show -json in each unit with the unit's own key configuration.
---

# Read OpenTofu encrypted state

OpenTofu can encrypt state on the client before it writes it to S3. tfcensus reads state objects
itself, and it doesn't hold your keys, so by default it can read only the unencrypted envelope of
such a state: its serial and lineage. `--decrypt-with-tofu` lets OpenTofu decrypt it.

## What you see without it

An encrypted state shows the status `encrypted` in `inventory`, with its serial, size and last
change, and no resource count:

```text
tf-fleet/secure: state is encrypted with OpenTofu client-side encryption, so only serial and lineage are readable; --decrypt-with-tofu reads it with tofu show -json in the unit
```

`scan` and `whois` can't match resources against a state they can't read. A resource that only an
encrypted state manages shows as `unmanaged` in a scan until the state is decrypted, and the scan
report doesn't name the encrypted state. Run `inventory` first to see which states are encrypted.

## Turn decryption on

Pass `--decrypt-with-tofu`, or set `state.decrypt_with_tofu: true` in the config
(`TFCENSUS_STATE_DECRYPT_WITH_TOFU=true` from the environment). `inventory`, `scan`, `whois` and
`audit backend` take the flag:

```sh
tfcensus inventory ~/src/infra/acme-live --decrypt-with-tofu
tfcensus scan ~/src/infra/acme-live --profile acme-nonprod --decrypt-with-tofu
```

tfcensus still reads every state from S3 first. Only for a state that turns out to be encrypted
does it run, in that unit's directory:

```text
tofu init -input=false -no-color -lock=false
tofu show -json -no-color
```

In a Terragrunt unit, both go through `terragrunt run --tf-path <tofu> -- ...`. tfcensus reads the
JSON from `tofu show` and keeps identifiers only, as it does for any state. The row then shows
`ok`, and `--format json` keeps `"encrypted": true` on it.

`--tofu-path` (config `binaries.tofu`, default `tofu`) names the binary. It needs OpenTofu 1.7 or
later. `doctor --decrypt-with-tofu` checks that it is installed and lists `state encryption` among
the features a version too old lacks.

The [scan cache](/concepts/caches#the-scan-cache) doesn't record whether a snapshot was made with
decryption on. When a scan without the flag ran less than `cache.ttl` ago, add `--refresh` to the
first scan with it.

## Keys and credentials

tofu decrypts with the key configuration the unit already uses: the `encryption` block in its
code, or `TF_ENCRYPTION` in the environment. tfcensus passes `TF_ENCRYPTION` and every other
`TF_*`, `TOFU_*` and `AWS_*` variable to tofu, so a passphrase in `TF_VAR_*` or a KMS key provider
that uses your AWS credentials works the same way it does in a plan.

tofu reaches the backend with its own credentials: the unit's backend configuration and the AWS
variables and profiles in the environment. The per-backend credentials that tfcensus picks for
reading state ([how state is read](/concepts/state#credentials-per-backend)) don't apply to it. Run
tfcensus with the same `AWS_PROFILE` you would use for `tofu plan` in that unit.

[Terragrunt and terraform](/concepts/terragrunt#the-environment-of-a-child-process) lists every
variable that reaches a child process.

## What it changes in the unit

Nothing. Each call gets its own `TF_DATA_DIR` and terragrunt download directory in a temporary
folder, which tfcensus deletes afterwards, and it puts the unit's `.terraform.lock.hcl` back as it
was. `tofu init` runs with `-lock=false` and `tofu show` takes no lock, so no state lock is taken
and no state is written.

`tofu init` does download the unit's providers and modules, and `tofu show` runs the providers.
That is slower than reading S3, and it needs network access to the registries or a provider
mirror. The flag is off by default for that reason.

## When decryption fails

A state that tofu can't decrypt fails its unit, and the run goes on with the others. The error
says which step failed and what to check, without tofu's own output, which can hold attribute
values:

| Failed step | Check |
|---|---|
| `tofu init` | the unit's backend configuration and the credentials tofu uses |
| `tofu show -json` | the key configuration in the `encryption` block or `TF_ENCRYPTION` |
| binary not found | install OpenTofu, or set `--tofu-path`; for a Terragrunt unit, also `--tg-path` |

Run the same `tofu show -json` in the unit yourself to see tofu's message. `inventory` exits 1 when
any state failed ([exit codes](/reference/exit-codes)).
