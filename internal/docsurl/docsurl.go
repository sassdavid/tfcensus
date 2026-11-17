package docsurl

import "strings"

// Base is the docs site's URL, without a trailing slash.
const Base = "https://tfcensus.sasskovacs.dev"

// Page returns the URL of the site page at path, such as "reference/config" or
// "reference/config#cache-ttl". Pass a string literal: a test checks every path against the site.
func Page(path string) string {
	return Base + "/" + strings.TrimPrefix(path, "/")
}

// Schema returns the URL that the JSON schema file name is served at, which is also its $id.
func Schema(name string) string {
	return Base + "/schemas/" + name
}
