// Package fakebin turns the test binary into fake terragrunt, terraform or tofu executables. A
// sidecar spec next to each link says what the fake prints and how it exits, and every call is recorded.
package fakebin
