// Package suggest finds resource moves in plans by pairing each planned delete with the one planned
// create of the same type whose known attributes are equal, and drafts a migration file from them.
package suggest
