// Package drift reads terraform plans written by terragrunt run --all --json-out-dir, diffs the
// drifted resources at attribute level, hides noise and collapses identical changes across units.
package drift
