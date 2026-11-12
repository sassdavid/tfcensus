// Package migrate moves resources between the states of Terragrunt units: it parses migration
// files, edits state JSON, verifies the result with plans, and pushes, records and rolls back the
// changes. It is the only package that writes to S3, DynamoDB or state.
package migrate
