package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/mcpserver"
)

// mcpListing is what the MCP server reports about itself to a client.
type mcpListing struct {
	instructions string
	tools        []*mcp.Tool
	prompts      []*mcp.Prompt
}

// listMCP starts the MCP server in process with no backend, connects a client over the SDK's
// in-memory transport, and lists the tools and prompts.
func listMCP(ctx context.Context) (*mcpListing, error) {
	s := mcpserver.New(mcpserver.Options{
		Version: "docs",
		Logger:  slog.New(slog.DiscardHandler),
		Clock:   func() time.Time { return time.Unix(0, 0).UTC() },
	})
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, serverT)
	if err != nil {
		return nil, fmt.Errorf("start mcp server: %w", err)
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "gendocs", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		return nil, fmt.Errorf("connect mcp client: %w", err)
	}
	defer func() { _ = cs.Close() }()
	out := &mcpListing{instructions: cs.InitializeResult().Instructions}
	for tool, terr := range cs.Tools(ctx, nil) {
		if terr != nil {
			return nil, fmt.Errorf("list tools: %w", terr)
		}
		out.tools = append(out.tools, tool)
	}
	for p, perr := range cs.Prompts(ctx, nil) {
		if perr != nil {
			return nil, fmt.Errorf("list prompts: %w", perr)
		}
		out.prompts = append(out.prompts, p)
	}
	return out, nil
}

func (g *generator) mcpPage(ctx context.Context) error {
	l, err := listMCP(ctx)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(frontmatter("MCP tools and prompts", "The tools and prompts that "+config.AppName+
		" mcp serves, with their input schemas."))
	b.WriteString("# MCP tools and prompts\n\n")
	b.WriteString("`" + config.AppName + " mcp` serves these tools and prompts over stdio. This page " +
		"comes from the server's own `tools/list` and `prompts/list` answers. See " +
		"[" + config.AppName + " mcp](/cli/mcp) for the flags.\n\n")
	if l.instructions != "" {
		b.WriteString("## Server instructions\n\nThe server sends this text to the client when it connects:\n\n" +
			"```text\n" + strings.TrimSpace(l.instructions) + "\n```\n\n")
	}
	b.WriteString("## Tools\n\n")
	for _, t := range l.tools {
		b.WriteString("### " + t.Name + "\n\n")
		if t.Title != "" {
			b.WriteString(cell(t.Title) + ". ")
		}
		if t.Annotations != nil && t.Annotations.ReadOnlyHint {
			b.WriteString("Read-only.")
		}
		b.WriteString("\n\n" + prose(t.Description) + "\n\n")
		schema, merr := json.MarshalIndent(t.InputSchema, "", "  ")
		if merr != nil {
			return fmt.Errorf("encode %s input schema: %w", t.Name, merr)
		}
		b.WriteString("Input schema:\n\n```json\n" + string(schema) + "\n```\n\n")
	}
	if len(l.prompts) > 0 {
		b.WriteString("## Prompts\n\n")
	}
	for _, p := range l.prompts {
		b.WriteString("### " + p.Name + "\n\n")
		if p.Title != "" {
			b.WriteString(cell(p.Title) + ".\n\n")
		}
		b.WriteString(prose(p.Description) + "\n\n")
		if len(p.Arguments) > 0 {
			rows := make([][]string, len(p.Arguments))
			for i, a := range p.Arguments {
				rows[i] = []string{code(a.Name), yesNo(a.Required), cell(a.Description)}
			}
			b.WriteString(table([]string{"Argument", "Required", colDescription}, rows) + "\n")
		}
	}
	g.add("reference/mcp.md", []byte(strings.TrimRight(b.String(), "\n")+"\n"))
	return nil
}
