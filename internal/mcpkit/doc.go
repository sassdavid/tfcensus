// Package mcpkit runs a stdio MCP server whose tools are read-only and answer with the JSON
// envelope of --format json, split into pages with an opaque cursor when an answer passes the
// size cap.
package mcpkit
