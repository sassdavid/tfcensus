// Package config loads layered configuration and records where each value came from.
//
// Layers, lowest precedence first: defaults, user file, project file, TFCENSUS_* env vars,
// flags. Files are checked against schemas/config-v1.json before they are merged.
package config
