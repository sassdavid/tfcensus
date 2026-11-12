package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sassdavid/tfcensus/internal/report"
)

// MaxBody is the most characters a body has. GitHub refuses issue bodies over 65536 characters.
const MaxBody = 60_000

// Marker is the hidden first line of every issue body and comment tfcensus writes. Kind and Scope
// find the issue again; Hash says whether the body changed.
type Marker struct {
	Kind  report.Kind `json:"kind"`
	Scope string      `json:"scope"`
	Hash  string      `json:"hash"`
}

const markerPrefix = "<!-- tfcensus:v1 "

// markerRoom is the space a body keeps for its marker line.
const markerRoom = 200

var markerRE = regexp.MustCompile(`<!-- tfcensus:v1 (\{[^\n]*?\}) -->`)

// String renders the marker as an HTML comment, which GitHub and Bitbucket don't show.
func (m Marker) String() string {
	b, _ := json.Marshal(m) //nolint:errchkjson // three strings always encode
	// A scope with "--" could end the comment early; JSON reads \u002d back as "-".
	return markerPrefix + strings.ReplaceAll(string(b), "--", `-\u002d`) + " -->"
}

// ParseMarker finds the marker in a body.
func ParseMarker(body string) (Marker, bool) {
	sub := markerRE.FindStringSubmatch(body)
	if sub == nil {
		return Marker{}, false
	}
	var m Marker
	if err := json.Unmarshal([]byte(sub[1]), &m); err != nil || m.Kind == "" || m.Scope == "" {
		return Marker{}, false
	}
	return m, true
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:12])
}

// withMarker prefixes text with its marker.
func withMarker(kind report.Kind, scope, text string) (string, Marker) {
	m := Marker{Kind: kind, Scope: scope, Hash: hash(text)}
	return m.String() + "\n\n" + text, m
}

func cell(s string) string {
	s = strings.NewReplacer("|", `\|`, "\n", " ", "\r", " ", "`", "'").Replace(s)
	return s
}

func kindNoun(k report.Kind) string {
	switch k {
	case report.KindScan:
		return "unmanaged or double-managed resources"
	case report.KindDrift:
		return "drifted resources"
	case report.KindAudit:
		return "backend audit findings"
	case report.KindIndex:
	}
	return "findings"
}

// ScopeMarkdown renders one scope as Markdown, at most limit characters.
func ScopeMarkdown(kind report.Kind, s *Scope, heading string, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n\n", heading, s.Title)
	if s.Failed {
		fmt.Fprintf(&b, "The plan failed, so drift is unknown: %s\n\n", cell(s.Reason))
	}
	if len(s.Items) == 0 {
		if !s.Failed {
			b.WriteString("Nothing to report.\n")
		}
		return b.String()
	}
	noun := kindNoun(kind)
	fmt.Fprintf(&b, "%s%s: %d.\n\n", strings.ToUpper(noun[:1]), noun[1:], len(s.Items))
	header := "| Level | Type | Target | Region | Rule | Detail |\n|---|---|---|---|---|---|\n"
	b.WriteString(header)
	footer := func(n int) string { return fmt.Sprintf("\n...and %d more. Run tfcensus for the full list.\n", n) }
	for i, it := range s.Items {
		row := fmt.Sprintf("| %s | %s | `%s` | %s | %s | %s |\n",
			cell(it.Level), cell(it.Type), cell(it.Target), cell(it.Region), cell(it.Rule), cell(it.Detail))
		if b.Len()+len(row)+len(footer(len(s.Items))) > limit {
			b.WriteString(footer(len(s.Items) - i))
			break
		}
		b.WriteString(row)
	}
	return b.String()
}

const issueFooter = "\n---\nWritten by [tfcensus](https://github.com/sassdavid/tfcensus). The next run updates this " +
	"issue, and closes it when it finds nothing in this scope. Edits to this body are overwritten.\n"

// IssueBody renders the issue body of one scope, marker first.
func IssueBody(kind report.Kind, s *Scope) (string, Marker) {
	text := ScopeMarkdown(kind, s, "###", MaxBody-len(issueFooter)-markerRoom) + issueFooter
	return withMarker(kind, s.Key, text)
}

// ReportMarkdown renders every open scope of a report as one Markdown document.
func ReportMarkdown(r *Report, limit int) string {
	var b strings.Builder
	open := r.OpenScopes()
	fmt.Fprintf(&b, "## tfcensus %s\n\n", r.Kind)
	if len(open) == 0 {
		fmt.Fprintf(&b, "Nothing to report in %d scope(s).\n", len(r.Scopes))
		return b.String()
	}
	fmt.Fprintf(&b, "%d of %d scope(s) have something to report.\n\n", len(open), len(r.Scopes))
	for i := range open {
		left := limit - b.Len()
		if left < 500 { //nolint:mnd // room for one scope's heading and a few rows
			fmt.Fprintf(&b, "...and %d more scope(s). Run tfcensus for the full list.\n", len(open)-i)
			break
		}
		b.WriteString(ScopeMarkdown(r.Kind, &open[i], "###", left/max(len(open)-i, 1)))
		b.WriteString("\n")
	}
	return b.String()
}
