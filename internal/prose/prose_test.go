package prose_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/prose"
)

func TestCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file string
		text string
		want []string
	}{
		{"clean", "a.md", "tfcensus reads mise.toml and prints a table.", nil},
		{"word", "a.md", "A robust parser.", []string{`a.md:1:3: avoid "robust", say it plainly`}},
		{"word case and boundary", "a.go", "// Leverage it. Leveraged is fine? leverages", []string{
			`a.go:1:4: avoid "Leverage", say it plainly`,
			`a.go:1:36: avoid "leverages", say it plainly`,
		}},
		{"phrase", "a.md", "In summary, it works.", []string{`a.md:1:1: avoid the phrase "In summary"`}},
		{"em dash", "a.md", "fast — and small", []string{
			`a.md:1:6: no em or en dashes ("—"): use a comma, colon, parentheses or a hyphen`,
		}},
		{"curly quote", "a.md", "it’s", []string{`a.md:1:3: use straight quotes, not "’"`}},
		{"emoji", "a.md", "done 🚀", []string{`a.md:1:6: no emoji ("🚀")`}},
		{"bold label in md", "a.md", "- **Speed:** fast", []string{
			`a.md:1:3: list item starts with a bold label **Speed:**: write a sentence or use a table`,
		}},
		{"bold label outside md is fine", "a.go", "// - **Speed:** fast", nil},
		{"ignore marker", "a.md", "robust <!-- prose:ignore -->", nil},
		{"second line", "a.md", "ok\nseamless", []string{`a.md:2:1: avoid "seamless", say it plainly`}},
	}
	c := prose.NewChecker()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, f := range c.Check(tt.file, []byte(tt.text)) {
				got = append(got, f.String())
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// The rule file must name every word and phrase the checker rejects.
func TestRulesFileListsEveryTerm(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("..", "..", ".claude", "rules", "writing.md"))
	require.NoError(t, err)
	rules := strings.ToLower(string(b))
	for _, term := range append(prose.Words(), prose.Phrases()...) {
		assert.Contains(t, rules, term, "add %q to .claude/rules/writing.md", term)
	}
}
