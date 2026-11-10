// Package execx runs external tools such as terragrunt, terraform, tofu and mise. It handles
// timeouts, one interrupt and then a kill on cancellation, an environment allowlist, and debug logs
// with secrets redacted.
package execx
