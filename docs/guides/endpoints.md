---
description: Point tfcensus at an AWS emulator with AWS_ENDPOINT_URL, and run it behind an HTTPS proxy or a TLS-inspecting proxy with its own CA certificate.
---

# Run against an emulator or through a proxy

tfcensus calls AWS through the AWS SDK for Go, so the SDK's endpoint, proxy and certificate
settings work. This page covers the ones that matter for an emulator such as LocalStack or a moto
server, and for a corporate proxy.

## Use an emulator

Set `AWS_ENDPOINT_URL` to send every AWS call to the emulator, or `AWS_ENDPOINT_URL_S3` to send
only S3 calls there. Give the emulator a profile with the dummy credentials it accepts:

```ini
# ~/.aws/config
[profile acme-local]
region = us-east-1
aws_access_key_id = test
aws_secret_access_key = test
```

```sh
export AWS_ENDPOINT_URL=http://localhost:4566
tfcensus units tf-fleet
tfcensus inventory tf-fleet --profile acme-local
tfcensus scan tf-fleet --profile acme-local --layers native
```

When `AWS_ENDPOINT_URL` or `AWS_ENDPOINT_URL_S3` is set, tfcensus switches the S3 clients that
read and write state to path-style addressing (`http://localhost:4566/<bucket>/<key>`), because an
emulator can't serve a bucket name as a host name. That covers `inventory`, `audit backend`,
`move` and the state reads of every other command. `scan`'s native lister of bucket settings
(policy, versioning, lifecycle, encryption) still uses host-style addressing. The SDK's
other service variables, such as `AWS_ENDPOINT_URL_STS`, work too, but don't switch S3 to
path-style. An `endpoint_url` in the profile doesn't switch it either, so set the variable when
the emulator serves S3.

Things to check against an emulator:

- `doctor` probes Resource Explorer, AWS Config and Cloud Control, which emulators often implement
  in part or not at all. `--layers native` limits `scan` to tfcensus's own listers, which call each
  service's list and describe APIs.
- tfcensus learns each profile's account with `sts:GetCallerIdentity`. The emulator's answer must
  be the account your units name, or `scan` can't match their states to the live resources.
- `--org` needs the emulator to implement AWS Organizations.

## The same endpoint for terraform and terragrunt

`AWS_ENDPOINT_URL` and every other `AWS_*` variable reach the terragrunt, terraform and tofu
processes that tfcensus starts, so `drift`, `move` and `terragrunt render` talk to the same
emulator. terraform's `s3` backend and the AWS provider need their own path-style setting:
`use_path_style = true` in the backend and `s3_use_path_style = true` in the provider. The
[terragrunt concept page](/concepts/terragrunt#the-environment-of-a-child-process) lists the
variables a child process gets.

## Go through a proxy

tfcensus reads the standard proxy variables `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` (or their
lower-case forms) for AWS calls and for `notify`'s requests to Slack, GitHub and Bitbucket:

```sh
export HTTPS_PROXY=http://proxy.acme.internal:3128
export NO_PROXY=169.254.169.254,localhost
tfcensus scan --profile acme-nonprod
```

Keep `169.254.169.254` in `NO_PROXY` on EC2 or in a container that gets credentials from the
instance metadata service. The proxy variables also reach terragrunt and terraform.

## Trust a proxy's CA certificate

A proxy that inspects TLS presents certificates signed by its own CA. Each kind of request
needs to trust that CA:

| Requests | Setting |
|---|---|
| AWS calls | `AWS_CA_BUNDLE=/path/to/proxy-ca.pem`, or `ca_bundle` in the profile, which the AWS SDK reads on every OS |
| `notify` (Slack, GitHub, Bitbucket) | `SSL_CERT_FILE=/path/to/proxy-ca.pem`: on macOS and Windows, tfcensus adds those certificates to the system ones |
| terraform, terragrunt and tofu | `SSL_CERT_FILE`, `SSL_CERT_DIR` and `AWS_CA_BUNDLE` reach them; each tool reads them its own way |

On Linux, Go reads `SSL_CERT_FILE` and `SSL_CERT_DIR` in place of the system certificates for
every connection, AWS calls included. Point them at a bundle that holds the system CAs and the
proxy's, or use `AWS_CA_BUNDLE` for AWS and leave `SSL_CERT_FILE` unset.

## Related

- [Environment variables](/reference/environment) lists every variable tfcensus reads.
- [Troubleshooting](/troubleshooting) covers credential and access errors.
