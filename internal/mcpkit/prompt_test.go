package mcpkit_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sassdavid/tfcensus/internal/mcpkit"
	"github.com/sassdavid/tfcensus/internal/testutil/mcptest"
)

// newPromptServer returns a server with a "review" prompt that echoes its arguments and a
// "broken" prompt whose func fails.
func newPromptServer(t *testing.T) *mcpkit.Server {
	t.Helper()
	s := mcpkit.NewServer(mcpkit.Options{Name: "kit", Version: "1.2.3"})
	mcpkit.AddPrompt(s, mcpkit.Prompt{
		Name:        "review",
		Title:       "Review",
		Description: "Reviews a tool.",
		Arguments: []mcpkit.PromptArgument{
			{Name: "tool", Description: "Tool name.", Required: true},
			{Name: "to", Description: "Target version."},
		},
	}, func(args map[string]string) (string, error) {
		return fmt.Sprintf("review tool=%q to=%q n=%d", args["tool"], args["to"], len(args)), nil
	})
	mcpkit.AddPrompt(s, mcpkit.Prompt{Name: "broken", Description: "Fails."},
		func(map[string]string) (string, error) { return "", errBoom })
	return s
}

func TestPrompt_list(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newPromptServer(t))
	got := c.Prompts(t)
	want := []*mcp.Prompt{
		{Name: "broken", Description: "Fails."},
		{Name: "review", Title: "Review", Description: "Reviews a tool.", Arguments: []*mcp.PromptArgument{
			{Name: "tool", Description: "Tool name.", Required: true},
			{Name: "to", Description: "Target version."},
		}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("prompts (-want +got):\n%s", diff)
	}
}

func TestPrompt_get(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newPromptServer(t))
	tests := []struct {
		args map[string]string
		want string
	}{
		{map[string]string{"tool": "kubectl"}, `review tool="kubectl" to="" n=1`},
		{map[string]string{"tool": " kubectl ", "to": "1.37.2"}, `review tool="kubectl" to="1.37.2" n=2`},
		{map[string]string{"tool": "kubectl", "to": "  "}, `review tool="kubectl" to="" n=1`},
	}
	for _, tt := range tests {
		if got := c.Prompt(t, "review", tt.args); got != tt.want {
			t.Errorf("%v: text = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestPrompt_errors(t *testing.T) {
	t.Parallel()
	c := mcptest.Connect(t, newPromptServer(t))
	tests := []struct {
		name string
		args map[string]string
		want string
	}{
		{"review", nil, `missing required argument "tool"`},
		{"review", map[string]string{"tool": " "}, `missing required argument "tool"`},
		{"review", map[string]string{"tool": "kubectl", "from": "1.0", "b": "x"}, `unknown argument "b"`},
		{"broken", nil, "boom"},
		{"nope", nil, `unknown prompt "nope"`},
	}
	for _, tt := range tests {
		err := c.PromptError(t, tt.name, tt.args)
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s %v: error = %q, want %q", tt.name, tt.args, err, tt.want)
		}
	}
}

func TestAddPrompt_duplicateArgument(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "argument x declared twice") {
			t.Errorf("recover() = %v", r)
		}
	}()
	s := mcpkit.NewServer(mcpkit.Options{})
	mcpkit.AddPrompt(s, mcpkit.Prompt{Name: "p", Arguments: []mcpkit.PromptArgument{{Name: "x"}, {Name: "x"}}},
		func(map[string]string) (string, error) { return "", nil })
}
