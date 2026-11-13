package render

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DefaultMDMaxChars is the default --md-max-chars, under GitHub's 65,536-character cap on comments.
const DefaultMDMaxChars = 60_000

type blockKind int

const (
	blockText blockKind = iota // paragraphs, code and table headers
	blockSection
	blockRow
	blockItem
)

func (k blockKind) noun(n int) string {
	nouns := map[blockKind][2]string{
		blockText:    {"other line", "other lines"},
		blockSection: {"section", "sections"},
		blockRow:     {"table row", "table rows"},
		blockItem:    {"list item", "list items"},
	}[k]
	if n == 1 {
		return "1 " + nouns[0]
	}
	return fmt.Sprintf("%d %s", n, nouns[1])
}

// mdBlock is a run of lines that truncation keeps or drops whole.
type mdBlock struct {
	text  string
	kind  blockKind
	title string // the summary or heading text of a section
	chars int
	bare  bool // a heading with nothing under it yet, which the next block joins
}

var (
	listItemRE  = regexp.MustCompile(`^(?:[-*+]|\d{1,9}[.)])(?:\s|$)`)
	tableSepRE  = regexp.MustCompile(`^\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$`)
	summaryRE   = regexp.MustCompile(`<summary>(.*?)</summary>`)
	detailsOpen = regexp.MustCompile(`(?i)<details[\s>]`)
	detailsEnd  = regexp.MustCompile(`(?i)</details>`)
	headingRE   = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
)

// splitter cuts Markdown into blocks, one line at a time.
type splitter struct {
	blocks []mdBlock
	fence  string // the open code fence, ``` or ~~~
	depth  int    // open <details> elements
	prev   string // the previous line, trimmed
}

// splitMarkdown cuts md into blocks. A <details> element, a fenced code block and a heading with
// the text under it each stay in one block; table rows and list items are blocks of their own.
func splitMarkdown(md string) []mdBlock {
	var s splitter
	for line := range strings.Lines(md) {
		trimmed := strings.TrimSpace(line)
		if s.fence == "" && s.depth == 0 {
			s.open(line, trimmed)
		}
		cur := &s.blocks[len(s.blocks)-1]
		cur.text += line
		cur.chars += utf8.RuneCountInString(line)
		s.track(trimmed)
		s.prev = trimmed
	}
	return s.blocks
}

func isFence(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// open starts a new block when line begins one; otherwise the line joins the current block.
func (s *splitter) open(line, trimmed string) {
	prevRow := strings.HasPrefix(s.prev, "|")
	switch {
	case trimmed == "":
	case isFence(trimmed):
		s.start(blockText, "")
	case detailsOpen.MatchString(trimmed):
		title := ""
		if m := summaryRE.FindStringSubmatch(trimmed); m != nil {
			title = m[1]
		}
		s.start(blockSection, title)
	case headingRE.MatchString(trimmed):
		s.start(blockSection, headingRE.FindStringSubmatch(trimmed)[1])
		s.blocks[len(s.blocks)-1].bare = true
	case strings.HasPrefix(trimmed, "|") && prevRow && tableSepRE.MatchString(trimmed):
	case strings.HasPrefix(trimmed, "|") && prevRow:
		s.start(blockRow, "")
	case strings.HasPrefix(trimmed, "|"):
		s.start(blockText, "") // the header row, which the separator joins
	case listItemRE.MatchString(trimmed) && line[0] != ' ' && line[0] != '\t':
		s.start(blockItem, "")
	case s.prev == "" || prevRow:
		s.start(blockText, "")
	}
	if len(s.blocks) == 0 {
		s.start(blockText, "")
	}
}

func (s *splitter) start(kind blockKind, title string) {
	if n := len(s.blocks); n > 0 && s.blocks[n-1].bare {
		s.blocks[n-1].bare = false
		return
	}
	s.blocks = append(s.blocks, mdBlock{kind: kind, title: title})
}

// track follows code fences and <details> nesting after a line.
func (s *splitter) track(trimmed string) {
	switch {
	case s.fence != "":
		if strings.HasPrefix(trimmed, s.fence) && strings.Trim(trimmed, s.fence[:1]) == "" {
			s.fence = ""
		}
	case isFence(trimmed):
		s.fence = trimmed[:3]
	default:
		opened := len(detailsOpen.FindAllString(trimmed, -1))
		closed := len(detailsEnd.FindAllString(trimmed, -1))
		s.depth = max(s.depth+opened-closed, 0)
	}
}

// TruncateMarkdown returns md cut to at most limit characters (Unicode code points). It cuts
// between table rows, list items and sections, and ends with a footer that counts what was cut
// and names the first cut section. A limit of 0 or less, or md within the limit, returns md.
func TruncateMarkdown(md string, limit int, hint string) string {
	if limit <= 0 || utf8.RuneCountInString(md) <= limit {
		return md
	}
	blocks := splitMarkdown(md)
	prefix := make([]int, len(blocks)+1)
	for i, b := range blocks {
		prefix[i+1] = prefix[i] + b.chars
	}
	var cut cutCount
	footer := ""
	for keep := len(blocks) - 1; keep >= 0; keep-- {
		cut.add(&blocks[keep])
		footer = cut.footer(limit, hint)
		if prefix[keep]+utf8.RuneCountInString(footer) <= limit {
			var b strings.Builder
			for _, bl := range blocks[:keep] {
				b.WriteString(bl.text)
			}
			if strings.HasSuffix(b.String(), "\n\n") {
				footer = footer[1:] // the kept part already ends with a blank line
			}
			b.WriteString(footer)
			return b.String()
		}
	}
	return string([]rune(footer)[:limit])
}

// cutCount tallies the blocks truncation drops, added from the last one back.
type cutCount struct {
	counts map[blockKind]int
	title  string // the first dropped section with a title
}

const maxTitleChars = 80

func (c *cutCount) add(b *mdBlock) {
	if c.counts == nil {
		c.counts = make(map[blockKind]int)
	}
	if b.kind == blockText && (strings.TrimSpace(b.text) == "" || strings.HasPrefix(b.text, "|")) {
		return // blank lines and table headers
	}
	c.counts[b.kind]++
	if b.kind == blockSection && b.title != "" {
		c.title = b.title
	}
}

// footer says what was dropped, such as "3 sections and 120 table rows".
func (c *cutCount) footer(limit int, hint string) string {
	var parts []string
	for _, k := range []blockKind{blockSection, blockRow, blockItem, blockText} {
		if c.counts[k] > 0 {
			parts = append(parts, k.noun(c.counts[k]))
		}
	}
	what := "the end of the report"
	switch len(parts) {
	case 0:
	case 1:
		what = parts[0]
	default:
		what = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n---\n\nTruncated to %d characters: cut %s", limit, what)
	if c.title != "" {
		title := []rune(strings.ReplaceAll(c.title, "\n", " "))
		if len(title) > maxTitleChars {
			title = append(title[:maxTitleChars-3], '.', '.', '.')
		}
		fmt.Fprintf(&b, ", starting at %q", string(title))
	}
	b.WriteString(".")
	if hint != "" {
		b.WriteString(" " + hint)
	}
	b.WriteString("\n")
	return b.String()
}
