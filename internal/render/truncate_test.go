package render_test

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/render"
)

// genMarkdown builds a report from the shapes the renderers write: headings with text, tables,
// lists and <details> sections, with multi-byte characters so bytes and characters differ.
func genMarkdown(r *rand.Rand) string {
	words := []string{"bucket", "état", "日本語", "drift", "ünit", "`code`", "a|b", "<br>"}
	word := func() string { return words[r.IntN(len(words))] }
	var b strings.Builder
	for range r.IntN(12) + 1 {
		switch r.IntN(5) {
		case 0:
			fmt.Fprintf(&b, "## heading %s\n\nIntro %s %s.\n\n", word(), word(), word())
		case 1:
			b.WriteString("| A | B |\n|---|---|\n")
			for range r.IntN(30) {
				fmt.Fprintf(&b, "| row %s | %s |\n", word(), word())
			}
			b.WriteString("\n")
		case 2:
			for range r.IntN(8) + 1 {
				fmt.Fprintf(&b, "- item %s %s\n", word(), word())
			}
			b.WriteString("\n")
		case 3:
			fmt.Fprintf(&b, "<details><summary>sec %s</summary>\n\n", word())
			for range r.IntN(6) {
				fmt.Fprintf(&b, "- %s\n", word())
			}
			b.WriteString("\n```\nkeep\n\n</details> inside code\n```\n\n</details>\n\n")
		default:
			fmt.Fprintf(&b, "Paragraph %s %s.\nSecond line %s.\n\n", word(), word(), word())
		}
	}
	return b.String()
}

var footerRE = regexp.MustCompile(`\n---\n\nTruncated to (\d+) characters: cut ([^\n]*)\n$`)

func cutCount(what, noun string) int {
	m := regexp.MustCompile(`(\d+) ` + noun).FindStringSubmatch(what)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// TestTruncateMarkdownLimit is a property test: the output never exceeds the limit, a report at
// the limit is unchanged, one character over gets the footer, the kept part is a line-aligned
// prefix of the report, and the footer counts what the dropped part holds.
func TestTruncateMarkdownLimit(t *testing.T) {
	t.Parallel()
	for seed := range uint64(400) {
		r := rand.New(rand.NewPCG(seed, seed*31+7))
		md := genMarkdown(r)
		n := utf8.RuneCountInString(md)
		limits := []int{n, n + 1, n - 1, r.IntN(n) + 1, r.IntN(400) + 1, 1, 60}
		for _, limit := range limits {
			if limit <= 0 {
				continue
			}
			got := render.TruncateMarkdown(md, limit, "Run `tool --format json`.")
			gotN := utf8.RuneCountInString(got)
			require.LessOrEqual(t, gotN, limit, "seed %d limit %d", seed, limit)
			if n <= limit {
				require.Equal(t, md, got, "seed %d limit %d", seed, limit)
				continue
			}
			loc := footerRE.FindStringSubmatchIndex(got)
			if loc == nil {
				// The footer alone is longer than the limit, so it is cut short.
				require.Less(t, limit, 200, "seed %d limit %d: no footer in %q", seed, limit, got)
				continue
			}
			kept, rest := got[:loc[0]], md[loc[0]:]
			require.True(t, strings.HasPrefix(md, kept), "seed %d limit %d", seed, limit)
			require.True(t, kept == "" || strings.HasSuffix(kept, "\n"), "seed %d limit %d", seed, limit)
			assert.Equal(t, strconv.Itoa(limit), got[loc[2]:loc[3]])
			what := got[loc[4]:loc[5]]
			assert.Equal(
				t,
				strings.Count("\n"+rest, "\n| row "),
				cutCount(what, "table row"),
				"seed %d: %s",
				seed,
				what,
			)
			assert.Equal(t, strings.Count(rest, "<details>")+strings.Count(rest, "## heading"),
				cutCount(what, "section"), "seed %d: %s", seed, what)
		}
	}
}

func TestTruncateMarkdownFooter(t *testing.T) {
	t.Parallel()
	md := "## tfcensus drift\n\n2 groups.\n\n" +
		"<details><summary>G1: aws_s3_bucket update</summary>\n\n| A | B |\n|---|---|\n| x | y |\n\n</details>\n" +
		"<details><summary>G2: aws_iam_role delete</summary>\n\nbody\n\n</details>\n" +
		"| A |\n|---|\n| a long cell that pushes the report over 1 |\n| a long cell that pushes the report over 2 |\n" +
		"| a long cell that pushes the report over 3 |\n"
	assert.Equal(t, md, render.TruncateMarkdown(md, 0, ""))
	assert.Equal(t, md, render.TruncateMarkdown(md, utf8.RuneCountInString(md), ""))
	got := render.TruncateMarkdown(md, 300, "Run `tfcensus drift --format json` for the full report.")
	assert.Equal(t, "## tfcensus drift\n\n2 groups.\n\n"+
		"<details><summary>G1: aws_s3_bucket update</summary>\n\n| A | B |\n|---|---|\n| x | y |\n\n</details>\n"+
		"\n---\n\nTruncated to 300 characters: cut 1 section and 3 table rows, "+
		"starting at \"G2: aws_iam_role delete\". Run `tfcensus drift --format json` for the full report.\n", got)
	assert.Equal(t, "\n---\n", render.TruncateMarkdown(md, 5, ""))
}
