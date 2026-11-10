// Package tfstate reads raw Terraform and OpenTofu state from S3 without locking it, keeps only
// resource identifiers, and indexes them by identity, ARN and ID.
package tfstate
