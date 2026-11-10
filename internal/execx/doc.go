// Package execx runs terragrunt, terraform and tofu. It handles timeouts, one interrupt and then a
// kill on cancellation, an environment allowlist, and debug logs with secrets redacted.
package execx
