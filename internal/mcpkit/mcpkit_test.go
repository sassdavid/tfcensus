package mcpkit_test

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sassdavid/tfcensus/internal/mcpkit"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/testutil/mcptest"
)

type item struct {
	ID   int    `json:"id"`
	Rank int    `json:"rank"`
	Text string `json:"text"`
}

type listData struct {
	Name  string `json:"name"`
	Items []item `json:"items"`
}

type listInput struct {
	mcpkit.Paging

	N    int `json:"n"              jsonschema:"Number of items."`
	Size int `json:"size,omitempty" jsonschema:"Bytes of text per item."`
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var errBoom = errors.New("boom")

// newServer returns a server with a "list" tool that pages its items by rank, an "other" tool
// with the same input, a "fail" tool and a "roots" tool that returns the client's roots.
func newServer(t *testing.T, o mcpkit.Options) *mcpkit.Server {
	t.Helper()
	o.Name, o.Version = "kit", "1.2.3"
	s := mcpkit.NewServer(o)
	list := func(_ context.Context, _ *mcp.CallToolRequest, in listInput) (*mcpkit.Result, error) {
		data := listData{Name: "list"}
		for i := range in.N {
			data.Items = append(data.Items, item{ID: i, Rank: (in.N - i) % 3, Text: strings.Repeat("x", in.Size)})
		}
		env := render.NewEnvelope("1.2.3", time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), data)
		pager := mcpkit.SlicePager(data.Items, "items",
			func(it item) int { return it.Rank },
			func(keep []item) any { return listData{Name: data.Name, Items: keep} })
		return &mcpkit.Result{Envelope: env, Pager: pager}, nil
	}
	mcpkit.AddTool(s, mcpkit.Tool{Name: "list", Title: "List", Description: "Lists items."}, list)
	mcpkit.AddTool(s, mcpkit.Tool{Name: "other", Description: "Lists items too.", OpenWorld: true}, list)
	mcpkit.AddTool(s, mcpkit.Tool{Name: "fail", Description: "Fails."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcpkit.Result, error) {
			return nil, fmt.Errorf("fail: %w", errBoom)
		})
	mcpkit.AddTool(s, mcpkit.Tool{Name: "roots", Description: "Returns the client's roots.", Roots: true},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcpkit.Result, error) {
			roots, err := mcpkit.ClientRoots(req)
			if err != nil {
				return nil, err
			}
			return &mcpkit.Result{
				Envelope: render.NewEnvelope("1.2.3", time.Time{}, map[string]any{"roots": roots}),
			}, nil
		})
	return s
}

func TestServer_infoAndAnnotations(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newServer(t, mcpkit.Options{}))
	if info := c.ServerInfo(); info.Name != "kit" || info.Version != "1.2.3" {
		t.Errorf("server info = %+v", info)
	}
	tools := c.Tools(t)
	if len(tools) != 4 {
		t.Fatalf("got %d tools", len(tools))
	}
	for _, tool := range tools {
		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || !a.IdempotentHint || a.DestructiveHint == nil || *a.DestructiveHint ||
			a.OpenWorldHint == nil || *a.OpenWorldHint != (tool.Name == "other") {
			t.Errorf("%s annotations = %+v", tool.Name, a)
		}
	}
	list := tools[slices.IndexFunc(tools, func(tool *mcp.Tool) bool { return tool.Name == "list" })]
	schema, err := json.Marshal(list.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"cursor"`, `"size"`, `"n":{"description":"Required unless cursor is set. Number of items."`} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("input schema %s lacks %s", schema, want)
		}
	}
	if strings.Contains(string(schema), `"required"`) {
		t.Errorf("input schema %s requires properties a cursor call doesn't send", schema)
	}
}

func TestServer_missingArgument(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newServer(t, mcpkit.Options{}))
	for _, args := range []map[string]any{{}, {"size": 3}} {
		msg := mcptest.ErrorText(t, c.Call(t, "list", args))
		if msg != `missing required argument "n"` {
			t.Errorf("%v: message = %q", args, msg)
		}
	}
}

func TestServer_smallResultIsWhole(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newServer(t, mcpkit.Options{}))
	text := mcptest.Text(t, c.Call(t, "list", map[string]any{"n": 3}))
	if page := mcptest.PageOf(t, text); page != nil {
		t.Fatalf("page = %+v, want none", page)
	}
	if strings.Contains(string(text), "\n") {
		t.Errorf("result is not compact: %s", text)
	}
	if got := len(mcptest.Items(t, text, "/data/items")); got != 3 {
		t.Errorf("got %d items", got)
	}
}

func TestServer_pages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		max      int
		n, size  int
		minPages int
	}{
		{"many small items", 5_000, 200, 40, 3},
		{"default cap", 0, 400, 300, 3},
		{"item over the cap", 1_000, 3, 2_000, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := mcptest.Connect(t, newServer(t, mcpkit.Options{MaxBytes: tt.max}))
			args := map[string]any{"n": tt.n, "size": tt.size}
			pages := c.Pages(t, "list", args)
			if len(pages) < tt.minPages {
				t.Fatalf("got %d pages, want at least %d", len(pages), tt.minPages)
			}
			limit := cmp.Or(tt.max, mcpkit.MaxBytes)
			var seen []item
			lastRank := -1
			for i, p := range pages {
				items := checkListPage(t, i, p, i == len(pages)-1, tt.n, limit)
				for _, it := range items {
					if it.Rank < lastRank {
						t.Errorf("rank %d after rank %d: earlier ranks must fill earlier pages", it.Rank, lastRank)
					}
				}
				lastRank = slices.MaxFunc(items, func(a, b item) int { return a.Rank - b.Rank }).Rank
				seen = append(seen, items...)
			}
			checkIDs(t, seen, tt.n)
		})
	}
}

// checkIDs fails unless items holds each id from 0 to n-1 once.
func checkIDs(t *testing.T, items []item, n int) {
	t.Helper()
	got := make([]int, len(items))
	for i, it := range items {
		got[i] = it.ID
	}
	slices.Sort(got)
	want := make([]int, n)
	for i := range want {
		want[i] = i
	}
	if !slices.Equal(got, want) {
		t.Errorf("got ids %v across pages; want each of %d items once", got, n)
	}
}

// checkListPage checks page i of the list tool's result and returns its items.
func checkListPage(t *testing.T, i int, p []byte, last bool, total, limit int) []item {
	t.Helper()
	page := mcptest.PageOf(t, p)
	if page == nil || page.Items != "items" || page.Total != total {
		t.Fatalf("page %d = %+v", i, page)
	}
	if (page.NextCursor == "") != last {
		t.Errorf("page %d next_cursor = %q", i, page.NextCursor)
	}
	if strings.ContainsAny(page.NextCursor, "+/=") {
		t.Errorf("cursor %q is not URL-safe base64", page.NextCursor)
	}
	var env struct {
		GeneratedAt string   `json:"generated_at"`
		Data        listData `json:"data"`
	}
	if err := json.Unmarshal(p, &env); err != nil {
		t.Fatal(err)
	}
	if env.GeneratedAt != "2026-11-12T00:00:00Z" || env.Data.Name != "list" {
		t.Errorf("page %d lost the fixed fields: %s", i, env.GeneratedAt)
	}
	if len(env.Data.Items) != page.Returned || page.Returned == 0 {
		t.Errorf("page %d returned %d, has %d items", i, page.Returned, len(env.Data.Items))
	}
	if len(p) > limit && page.Returned > 1 {
		t.Errorf("page %d has %d bytes and %d items, over the %d-byte cap", i, len(p), page.Returned, limit)
	}
	if !slices.IsSortedFunc(env.Data.Items, func(a, b item) int { return a.ID - b.ID }) {
		t.Errorf("page %d is not in the original order", i)
	}
	return env.Data.Items
}

func TestServer_cursorErrors(t *testing.T) {
	t.Parallel()
	clk := &clock{now: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
	c := mcptest.Connect(t, newServer(t, mcpkit.Options{
		MaxBytes: 2_000, Clock: clk.Now, CursorTTL: time.Minute, MaxSnapshots: 2,
	}))
	args := map[string]any{"n": 50, "size": 100}
	first := func() string {
		t.Helper()
		return mcptest.PageOf(t, mcptest.Text(t, c.Call(t, "list", args))).NextCursor
	}

	cursor := first()
	again := mcptest.Text(t, c.Call(t, "list", map[string]any{"cursor": cursor}))
	if repeat := mcptest.Text(t, c.Call(t, "list", map[string]any{"cursor": cursor})); string(repeat) != string(again) {
		t.Error("the same cursor returned a different page")
	}

	tests := []struct {
		name, tool, cursor string
		advance            time.Duration
	}{
		{"garbage", "list", "not a cursor", 0},
		{"unknown id", "list", "AAAAAAAAAAAAAAAAAAAAAAAAAAA", 0},
		{"other tool", "other", cursor, 0},
		{"expired", "list", cursor, 2 * time.Minute},
	}
	for _, tt := range tests {
		clk.Add(tt.advance)
		msg := mcptest.ErrorText(t, c.Call(t, tt.tool, map[string]any{"cursor": tt.cursor}))
		if !strings.Contains(msg, "unknown or expired cursor") ||
			!strings.Contains(msg, "call "+tt.tool+" again without a cursor") {
			t.Errorf("%s: message = %q", tt.name, msg)
		}
	}

	oldest := first()
	clk.Add(time.Second)
	_ = first()
	clk.Add(time.Second)
	_ = first()
	msg := mcptest.ErrorText(t, c.Call(t, "list", map[string]any{"cursor": oldest}))
	if !strings.Contains(msg, "unknown or expired cursor") {
		t.Errorf("the least recently used snapshot survived: %q", msg)
	}
}

func TestServer_noCap(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newServer(t, mcpkit.Options{MaxBytes: -1}))
	text := mcptest.Text(t, c.Call(t, "list", map[string]any{"n": 400, "size": 300}))
	if page := mcptest.PageOf(t, text); page != nil || len(text) <= mcpkit.MaxBytes {
		t.Errorf("got %d bytes, page %+v; want the whole result", len(text), page)
	}
}

func TestServer_toolError(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newServer(t, mcpkit.Options{}))
	if msg := mcptest.ErrorText(t, c.Call(t, "fail", map[string]any{})); msg != "fail: boom" {
		t.Errorf("message = %q", msg)
	}
}

func TestClientRoots(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tests := []struct {
		name  string
		roots []string
		want  []string
	}{
		{"client without roots", nil, nil},
		{"client roots", []string{dir, filepath.Join(dir, "a b")}, []string{dir, filepath.Join(dir, "a b")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := mcptest.Connect(t, newServer(t, mcpkit.Options{}), tt.roots...)
			var env struct {
				Data struct {
					Roots []string `json:"roots"`
				} `json:"data"`
			}
			if err := json.Unmarshal(mcptest.Text(t, c.Call(t, "roots", map[string]any{})), &env); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(env.Data.Roots, tt.want) {
				t.Errorf("roots = %q, want %q", env.Data.Roots, tt.want)
			}
		})
	}
}

func TestFilePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		uri, goos, want string
		wantErr         bool
	}{
		{"file:///home/a/src", "linux", "/home/a/src", false},
		{"file://localhost/home/a", "darwin", "/home/a", false},
		{"file:///home/a%20b", "linux", "/home/a b", false},
		{"file:///C:/src/infra", "windows", `C:\src\infra`, false},
		{"file:///c%3A/src", "windows", `c:\src`, false},
		{"file://server/share/infra", "windows", `\\server\share\infra`, false},
		{"file://server/share", "linux", "", true},
		{"https://github.com/a", "linux", "", true},
		{"/home/a", "linux", "", true},
	}
	for _, tt := range tests {
		got, err := mcpkit.FilePath(tt.uri, tt.goos)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("FilePath(%q, %s) = %q, %v; want %q, error %v", tt.uri, tt.goos, got, err, tt.want, tt.wantErr)
		}
	}
}
