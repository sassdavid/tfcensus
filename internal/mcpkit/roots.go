package mcpkit

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rootsRequest is the input request ID under which [AddTool] asks for the client's roots.
const rootsRequest = "roots"

// ClientRoots returns the local directories the client listed as its roots for a tool with
// [Tool.Roots] set, or nil when the client doesn't support roots. Claude Code lists its launch
// directory and the directories added with --add-dir.
func ClientRoots(req *mcp.CallToolRequest) ([]string, error) {
	if req == nil || req.Params == nil {
		return nil, nil
	}
	res, ok := req.Params.InputResponses[rootsRequest].(*mcp.ListRootsResult) //nolint:staticcheck // see needsRoots
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(res.Roots))
	for _, r := range res.Roots {
		p, err := FilePath(r.URI, runtime.GOOS)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// needsRoots reports whether the call must first ask a client that supports roots for them.
// Roots are deprecated from protocol 2026-07-28 (SEP-2577) but clients still answer, and the
// SDK turns the input request into roots/list for clients on older versions.
func needsRoots(req *mcp.CallToolRequest) bool {
	if req.Session == nil || req.Params == nil {
		return false
	}
	if _, ok := req.Params.InputResponses[rootsRequest]; ok {
		return false
	}
	params := req.Session.InitializeParams()
	return params != nil && params.Capabilities != nil &&
		params.Capabilities.RootsV2 != nil //nolint:staticcheck // see above
}

// FilePath turns a file URI into a local path for goos: file:///home/a is /home/a,
// file:///C:/src is C:\src and file://host/share is \\host\share on Windows.
func FilePath(uri, goos string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", fmt.Errorf("root %q: not a file URI", uri)
	}
	p := u.Path
	if goos != "windows" {
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("root %q: remote host %s", uri, u.Host)
		}
		return p, nil
	}
	if u.Host != "" && u.Host != "localhost" {
		return `\\` + u.Host + strings.ReplaceAll(p, "/", `\`), nil
	}
	// /C:/src: drop the slash before the drive letter.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return strings.ReplaceAll(filepath.Clean(p), "/", `\`), nil
}
