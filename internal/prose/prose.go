package prose

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// IgnoreMarker on a line skips every check for that line.
const IgnoreMarker = "prose:ignore"

// Words returns the words the checker rejects as whole words, in any case.
// .claude/rules/writing.md lists the same words, and a test keeps them in sync.
func Words() []string {
	return []string{
		"additionally", "boasts", "bolstered", "comprehensive", "crucial", "delve", "delves",
		"delving", "effortless", "effortlessly", "elevate", "embark", "empower", "endeavor",
		"enhance", "enhances", "enhancing", "foster", "fostering", "garner", "groundbreaking",
		"holistic", "interplay", "intricate", "intricacies", "landscape", "leverage", "leverages",
		"leveraging", "meticulous", "meticulously", "multifaceted", "myriad", "noteworthy",
		"paradigm", "pivotal", "plethora", "realm", "robust", "seamless", "seamlessly",
		"showcase", "showcases", "showcasing", "streamline", "streamlined", "synergy", "tapestry",
		"testament", "transformative", "unleash", "utilize", "utilizes", "utilizing", "vibrant",
	}
}

// Phrases returns the phrases the checker rejects, in any case.
func Phrases() []string {
	return []string{
		"a testament to", "as an ai", "deep dive", "great question", "i hope this helps",
		"in conclusion", "in summary", "it is important to note", "it's important to note",
		"it's worth noting", "let me know", "not only", "plays a key role", "plays a vital role",
		"serves as", "stands as",
	}
}

// Finding is one rule violation.
type Finding struct {
	Path    string
	Line    int
	Column  int
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d:%d: %s", f.Path, f.Line, f.Column, f.Message)
}

type rule struct {
	re      *regexp.Regexp
	message string
	mdOnly  bool
}

// Checker holds compiled rules. Build one with [NewChecker].
type Checker struct {
	rules []rule
}

// NewChecker compiles the rules.
func NewChecker() *Checker {
	words := regexp.MustCompile(`(?i)\b(` + strings.Join(Words(), "|") + `)\b`)
	quoted := make([]string, len(Phrases()))
	for i, p := range Phrases() {
		quoted[i] = regexp.QuoteMeta(p)
	}
	phrases := regexp.MustCompile(`(?i)\b(` + strings.Join(quoted, "|") + `)\b`)
	return &Checker{rules: []rule{
		{re: words, message: "avoid %q, say it plainly"},
		{re: phrases, message: "avoid the phrase %q"},
		{
			re:      regexp.MustCompile("[—–]"),
			message: "no em or en dashes (%q): use a comma, colon, parentheses or a hyphen",
		},
		{re: regexp.MustCompile("[‘’“”]"), message: "use straight quotes, not %q"},
		{re: regexp.MustCompile("[\U0001F300-\U0001FAFF☀-⛿✀-➿]"), message: "no emoji (%q)"},
		{
			re:      regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(\*\*[^*]+\*\*)`),
			message: "list item starts with a bold label %s: write a sentence or use a table",
			mdOnly:  true,
		},
	}}
}

// Check returns the findings for one file. name decides whether Markdown-only rules apply.
func (c *Checker) Check(name string, data []byte) []Finding {
	isMD := strings.EqualFold(path.Ext(name), ".md")
	var out []Finding
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, IgnoreMarker) {
			continue
		}
		for _, r := range c.rules {
			if r.mdOnly && !isMD {
				continue
			}
			for _, m := range r.re.FindAllStringSubmatchIndex(line, -1) {
				start, end := m[0], m[1]
				if len(m) > 2 && m[2] >= 0 {
					start, end = m[2], m[3]
				}
				out = append(out, Finding{
					Path:    name,
					Line:    i + 1,
					Column:  utf8.RuneCountInString(line[:start]) + 1,
					Message: fmt.Sprintf(r.message, line[start:end]),
				})
			}
		}
	}
	return out
}
