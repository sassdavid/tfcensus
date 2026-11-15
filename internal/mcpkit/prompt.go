package mcpkit

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prompt describes a prompt template that a client offers its user, such as a slash command.
type Prompt struct {
	Name        string
	Title       string
	Description string // one sentence
	Arguments   []PromptArgument
}

// PromptArgument is one string argument of a [Prompt].
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// PromptFunc returns the text of a prompt's user message. args holds the arguments the client
// set, trimmed, without blank ones.
type PromptFunc func(args map[string]string) (string, error)

// ErrUnknownArgument is returned when a prompt gets an argument it doesn't declare.
var ErrUnknownArgument = errors.New("unknown argument")

// AddPrompt registers a prompt whose answer is one user message. A get with an unknown argument
// or without a required one fails before f runs, because the SDK checks neither. It panics when
// two arguments share a name.
func AddPrompt(s *Server, p Prompt, f PromptFunc) {
	declared := make(map[string]PromptArgument, len(p.Arguments))
	args := make([]*mcp.PromptArgument, len(p.Arguments))
	for i, a := range p.Arguments {
		if _, dup := declared[a.Name]; dup {
			panic(fmt.Sprintf("mcpkit: prompt %s: argument %s declared twice", p.Name, a.Name))
		}
		declared[a.Name] = a
		args[i] = &mcp.PromptArgument{Name: a.Name, Description: a.Description, Required: a.Required}
	}
	s.mcp.AddPrompt(
		&mcp.Prompt{Name: p.Name, Title: p.Title, Description: p.Description, Arguments: args},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			in, err := promptArgs(p.Arguments, declared, req.Params.Arguments)
			if err != nil {
				return nil, err
			}
			text, err := f(in)
			if err != nil {
				return nil, err
			}
			return &mcp.GetPromptResult{
				Description: p.Description,
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
			}, nil
		},
	)
}

// promptArgs checks raw against the declared arguments and drops blank values.
func promptArgs(
	order []PromptArgument,
	declared map[string]PromptArgument,
	raw map[string]string,
) (map[string]string, error) {
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		if _, ok := declared[name]; !ok {
			return nil, fmt.Errorf("%w %q", ErrUnknownArgument, name)
		}
	}
	in := make(map[string]string, len(raw))
	for _, a := range order {
		v := strings.TrimSpace(raw[a.Name])
		switch {
		case v != "":
			in[a.Name] = v
		case a.Required:
			return nil, fmt.Errorf("%w %q", ErrArgument, a.Name)
		}
	}
	return in, nil
}
