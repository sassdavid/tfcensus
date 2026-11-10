// Package vcr records HTTP interactions into go-vcr cassettes and replays them in tests. A
// [Scrubber] removes credentials before a cassette is saved and refuses to save one that still
// contains a secret.
package vcr
