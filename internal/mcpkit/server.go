package mcpkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sassdavid/tfcensus/internal/render"
)

// MaxBytes is the default cap on the JSON text of one tool result. Claude Code saves larger
// text results to a file instead of showing them to the model.
const MaxBytes = 50_000

const (
	defaultCursorTTL    = 15 * time.Minute
	defaultMaxSnapshots = 16
)

// Options configures a [Server].
type Options struct {
	Name         string // server_info name
	Version      string // server_info version
	Instructions string // sent to the client at initialization
	Logger       *slog.Logger
	Clock        func() time.Time
	MaxBytes     int           // 0 means [MaxBytes]; below 0 turns the cap off
	CursorTTL    time.Duration // how long a split result stays after its last use; 0 means 15 minutes
	MaxSnapshots int           // split results kept at once, least recently used dropped first; 0 means 16
}

// Server is an MCP server for read-only tools that answer with an envelope.
type Server struct {
	mcp   *mcp.Server
	pages *store
	max   int
}

// NewServer returns a server with no tools.
func NewServer(o Options) *Server {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = MaxBytes
	}
	if o.CursorTTL <= 0 {
		o.CursorTTL = defaultCursorTTL
	}
	if o.MaxSnapshots <= 0 {
		o.MaxSnapshots = defaultMaxSnapshots
	}
	return &Server{
		mcp: mcp.NewServer(
			&mcp.Implementation{Name: o.Name, Version: o.Version},
			&mcp.ServerOptions{Instructions: o.Instructions, Logger: o.Logger},
		),
		pages: newStore(o.Clock, o.CursorTTL, o.MaxSnapshots),
		max:   o.MaxBytes,
	}
}

// Run serves one client over in and out until the client disconnects or ctx is canceled.
// Neither stream is closed.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	err := s.mcp.Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
	// A client that closes stdin is done, not failing; the SDK reports that as io.EOF.
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return fmt.Errorf("serve mcp: %w", err)
}

// Connect serves one client over t and returns at once; tests pass an in-memory transport.
func (s *Server) Connect(ctx context.Context, t mcp.Transport) (*mcp.ServerSession, error) {
	ss, err := s.mcp.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("connect mcp: %w", err)
	}
	return ss, nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// Tool describes a read-only tool.
type Tool struct {
	Name        string
	Title       string
	Description string // one sentence
	OpenWorld   bool   // the tool reaches the network
	Roots       bool   // ask the client for its roots before each call; read them with [ClientRoots]
}

// Paging is embedded in the input of a tool whose result can be split.
type Paging struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"The next_cursor of an earlier result of this tool. Other arguments are then ignored."` //nolint:lll // struct tag
}

func (p Paging) pageCursor() string { return p.Cursor }

type cursorer interface{ pageCursor() string }

// ErrArgument is returned when a call lacks a required argument.
var ErrArgument = errors.New("missing required argument")

// requiredUnlessCursor marks the schema's required properties in their descriptions instead,
// since a call with a cursor needs none of them. A top-level anyOf would say the same, but the
// Anthropic API rejects anyOf at the top of a tool's input schema.
const requiredUnlessCursor = "Required unless cursor is set."

// inputSchema returns In's input schema and, for a tool that pages, the properties a call
// without a cursor needs.
func inputSchema[In any]() (*jsonschema.Schema, []string, error) {
	schema, err := jsonschema.For[In](&jsonschema.ForOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("input schema: %w", err)
	}
	var zero In
	if _, paged := any(zero).(cursorer); !paged {
		return schema, nil, nil
	}
	required := schema.Required
	schema.Required = nil
	for _, name := range required {
		if p := schema.Properties[name]; p != nil {
			p.Description = strings.TrimSpace(requiredUnlessCursor + " " + p.Description)
		}
	}
	return schema, required, nil
}

// checkRequired returns [ErrArgument] when args lacks one of required or sets it to null, "" or [].
func checkRequired(args json.RawMessage, required []string) error {
	var m map[string]json.RawMessage
	if len(args) > 0 {
		if err := json.Unmarshal(args, &m); err != nil {
			return fmt.Errorf("decode arguments: %w", err)
		}
	}
	for _, name := range required {
		switch strings.TrimSpace(string(m[name])) {
		case "", "null", `""`, "[]":
			return fmt.Errorf("%w %q", ErrArgument, name)
		}
	}
	return nil
}

// Result is a whole answer before it is split.
type Result struct {
	Envelope render.Envelope
	Pager    Pager // nil when the data is never split
}

// Handler answers one call. Its error becomes a tool error, which the client shows to the model.
type Handler[In any] func(ctx context.Context, req *mcp.CallToolRequest, in In) (*Result, error)

// AddTool registers a tool as read-only, idempotent and not destructive. A call with a cursor
// gets the next page of an earlier result and never reaches h. It panics when In has no valid
// input schema, as [mcp.AddTool] does.
func AddTool[In any](s *Server, t Tool, h Handler[In]) {
	schema, required, schemaErr := inputSchema[In]()
	if schemaErr != nil {
		panic(fmt.Sprintf("mcpkit: tool %s: %v", t.Name, schemaErr))
	}
	notDestructive, openWorld := false, t.OpenWorld
	tool := &mcp.Tool{
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:           t.Title,
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &notDestructive,
			OpenWorldHint:   &openWorld,
		},
	}
	mcp.AddTool(
		s.mcp,
		tool,
		func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			if c, ok := any(in).(cursorer); ok && c.pageCursor() != "" {
				text, err := s.pages.next(t.Name, c.pageCursor())
				if err != nil {
					return nil, nil, err
				}
				return textResult(text), nil, nil
			}
			if err := checkRequired(req.Params.Arguments, required); err != nil {
				return nil, nil, err
			}
			if t.Roots && needsRoots(req) {
				return &mcp.CallToolResult{
					InputRequests: mcp.InputRequestMap{
						rootsRequest: &mcp.ListRootsParams{}, //nolint:staticcheck // roots are deprecated; see needsRoots
					},
				}, nil, nil
			}
			res, err := h(ctx, req, in)
			if err != nil {
				return nil, nil, err
			}
			text, err := s.pages.first(t.Name, res, s.max)
			if err != nil {
				return nil, nil, err
			}
			return textResult(text), nil, nil
		},
	)
}

func textResult(text []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}
}
