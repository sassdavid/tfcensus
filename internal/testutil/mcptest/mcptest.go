package mcptest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sassdavid/tfcensus/internal/mcpkit"
	"github.com/sassdavid/tfcensus/internal/render"
)

// maxPages stops [Client.Pages] from following a cursor loop forever.
const maxPages = 1000

// Client is an MCP client session connected to a server in the same process.
type Client struct {
	session *mcp.ClientSession
}

// Connect serves s over the SDK's in-memory transport and connects a client to it. With roots,
// the client supports roots and lists those directories. Both ends close when the test ends.
func Connect(t testing.TB, s *mcpkit.Server, roots ...string) *Client {
	t.Helper()
	ctx := t.Context()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, serverT)
	if err != nil {
		t.Fatalf("mcptest: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcptest", Version: "0"}, nil)
	for _, r := range roots {
		client.AddRoots(&mcp.Root{URI: FileURI(r)}) //nolint:staticcheck // servers still ask for roots
	}
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("mcptest: connect client: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})
	return &Client{session: cs}
}

// FileURI returns the file URI of a local path: /home/a is file:///home/a and C:\src is
// file:///C:/src.
func FileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// ServerInfo returns the name and version the server sent at initialization.
func (c *Client) ServerInfo() *mcp.Implementation {
	return c.session.InitializeResult().ServerInfo
}

// Tools lists the server's tools.
func (c *Client) Tools(t testing.TB) []*mcp.Tool {
	t.Helper()
	var out []*mcp.Tool
	for tool, err := range c.session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatalf("mcptest: list tools: %v", err)
		}
		out = append(out, tool)
	}
	return out
}

// Call calls a tool. A protocol error fails the test; a tool error is returned in the result.
func (c *Client) Call(t testing.TB, tool string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := c.session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("mcptest: call %s: %v", tool, err)
	}
	return res
}

// Text returns the text of a result that isn't a tool error.
func Text(t testing.TB, res *mcp.CallToolResult) []byte {
	t.Helper()
	text := allText(res)
	if res.IsError {
		t.Fatalf("mcptest: tool error: %s", text)
	}
	return []byte(text)
}

// ErrorText returns the message of a tool error, and fails the test for any other result.
func ErrorText(t testing.TB, res *mcp.CallToolResult) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("mcptest: want a tool error, got %.200s", allText(res))
	}
	return allText(res)
}

func allText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Pages calls a tool and follows next_cursor until the last page, returning every page.
func (c *Client) Pages(t testing.TB, tool string, args any) [][]byte {
	t.Helper()
	var pages [][]byte
	text := Text(t, c.Call(t, tool, args))
	for {
		pages = append(pages, text)
		page := PageOf(t, text)
		if page == nil || page.NextCursor == "" {
			return pages
		}
		if len(pages) == maxPages {
			t.Fatalf("mcptest: %s returned more than %d pages", tool, maxPages)
		}
		text = Text(t, c.Call(t, tool, map[string]any{"cursor": page.NextCursor}))
	}
}

// PageOf returns the page field of an envelope, or nil when the result wasn't split.
func PageOf(t testing.TB, envelope []byte) *render.Page {
	t.Helper()
	var env struct {
		Page *render.Page `json:"page"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		t.Fatalf("mcptest: decode envelope: %v", err)
	}
	return env.Page
}

// Indent formats a compact envelope the way --format json writes it.
func Indent(t testing.TB, envelope []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, envelope, "", "  "); err != nil {
		t.Fatalf("mcptest: indent: %v", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

// Golden calls a tool and compares its result, indented, with the --format json output in the
// golden file at path. The result must fit in one page. Golden files belong to the CLI tests,
// so this never rewrites them.
func (c *Client) Golden(t testing.TB, tool string, args any, path string) {
	t.Helper()
	text := Text(t, c.Call(t, tool, args))
	if page := PageOf(t, text); page != nil {
		t.Fatalf("mcptest: %s split its result (%d of %d %s); use a server without a cap",
			tool, page.Returned, page.Total, page.Items)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("mcptest: read golden: %v", err)
	}
	if diff := cmp.Diff(string(want), string(Indent(t, text))); diff != "" {
		t.Errorf("%s result differs from %s (-golden +mcp):\n%s", tool, path, diff)
	}
}

// Items returns the array at an RFC 6901 JSON pointer, such as "/data/tools/0/entries".
func Items(t testing.TB, doc []byte, pointer string) []json.RawMessage {
	t.Helper()
	cur := json.RawMessage(doc)
	if pointer != "" {
		for tok := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
			tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
			cur = step(t, cur, tok, pointer)
		}
	}
	var items []json.RawMessage
	if err := json.Unmarshal(cur, &items); err != nil {
		t.Fatalf("mcptest: %s is not an array: %v", pointer, err)
	}
	return items
}

func step(t testing.TB, cur json.RawMessage, tok, pointer string) json.RawMessage {
	t.Helper()
	if i, err := strconv.Atoi(tok); err == nil {
		var arr []json.RawMessage
		if json.Unmarshal(cur, &arr) == nil {
			if i < 0 || i >= len(arr) {
				t.Fatalf("mcptest: %s: index %d out of range", pointer, i)
			}
			return arr[i]
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(cur, &obj); err != nil {
		t.Fatalf("mcptest: %s: %v", pointer, err)
	}
	next, ok := obj[tok]
	if !ok {
		t.Fatalf("mcptest: %s: no %q", pointer, tok)
	}
	return next
}
