// Package fakebin turns the test binary into fake executables such as terragrunt or mise. A sidecar
// spec next to each link says what the fake prints and how it exits, and every call is recorded.
package fakebin
