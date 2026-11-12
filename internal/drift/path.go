package drift

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/sassdavid/tfcensus/internal/iampolicy"
)

// step is one element of an attribute path: an attribute or map key, or a list index.
type step struct {
	key   string
	index int
	isIdx bool
}

type path []step

func (p path) child(key string) path { return append(slices.Clip(p), step{key: key}) }

func (p path) item(i int) path { return append(slices.Clip(p), step{index: i, isIdx: true}) }

func identLike(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		letter := r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// format renders p as tags_all.Owner, ingress[0].cidr_blocks or tags["kubernetes.io/role"]. With
// shape, list indices become [*].
func (p path) format(shape bool) string {
	var b strings.Builder
	for i, s := range p {
		switch {
		case s.isIdx && shape:
			b.WriteString("[*]")
		case s.isIdx:
			b.WriteString("[" + strconv.Itoa(s.index) + "]")
		case identLike(s.key):
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.key)
		default:
			b.WriteString("[" + strconv.Quote(s.key) + "]")
		}
	}
	return b.String()
}

func (p path) String() string { return p.format(false) }

// hasPrefix reports whether q is a prefix of p.
func (p path) hasPrefix(q path) bool {
	if len(q) > len(p) {
		return false
	}
	for i := range q {
		if q[i] != p[i] {
			return false
		}
	}
	return true
}

// overlaps reports whether one path is a prefix of the other.
func (p path) overlaps(q path) bool { return p.hasPrefix(q) || q.hasPrefix(p) }

// patternStep is one element of an attribute pattern.
type patternStep struct {
	glob     string // for keys; * matches any characters
	index    int
	isIdx    bool
	anyIndex bool // [*]
}

// pattern is a parsed noise rule attribute. It matches a path it is a prefix of.
type pattern []patternStep

var errPattern = errors.New("invalid attribute pattern")

// parsePattern parses tags_all, tags_all.*, ingress[*].cidr_blocks or tags["kubernetes.io/role"].
func parsePattern(s string) (pattern, error) {
	var out pattern
	rest := s
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, "["):
			st, n, err := bracket(rest)
			if err != nil {
				return nil, fmt.Errorf("%w %q: %w", errPattern, s, err)
			}
			out, rest = append(out, st), rest[n:]
		case strings.HasPrefix(rest, "."):
			if len(out) == 0 {
				return nil, fmt.Errorf("%w %q: starts with a dot", errPattern, s)
			}
			rest = rest[1:]
			fallthrough
		default:
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			if end == 0 {
				return nil, fmt.Errorf("%w %q: empty name", errPattern, s)
			}
			out, rest = append(out, patternStep{glob: rest[:end]}), rest[end:]
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: empty", errPattern)
	}
	return out, nil
}

// bracket parses [*], [3] or ["key"] at the start of s and returns the step and its length.
func bracket(s string) (patternStep, int, error) {
	end := strings.IndexByte(s, ']')
	if strings.HasPrefix(s, `["`) {
		q, err := strconv.QuotedPrefix(s[1:])
		if err != nil || !strings.HasPrefix(s[1+len(q):], "]") {
			return patternStep{}, 0, errors.New("unterminated quoted key")
		}
		key, _ := strconv.Unquote(q)
		return patternStep{glob: key}, len(q) + 2, nil //nolint:mnd // the two brackets
	}
	if end < 0 {
		return patternStep{}, 0, errors.New("missing ]")
	}
	inner := s[1:end]
	if inner == "*" {
		return patternStep{anyIndex: true}, end + 1, nil
	}
	i, err := strconv.Atoi(inner)
	if err != nil || i < 0 {
		return patternStep{}, 0, fmt.Errorf("index %q: want a number, * or a quoted key", inner)
	}
	return patternStep{index: i, isIdx: true}, end + 1, nil
}

func (q pattern) matches(p path) bool {
	if len(q) > len(p) {
		return false
	}
	for i, ps := range q {
		s := p[i]
		switch {
		case ps.anyIndex:
			if !s.isIdx {
				return false
			}
		case ps.isIdx:
			if !s.isIdx || s.index != ps.index {
				return false
			}
		default:
			if s.isIdx || !iampolicy.Glob(ps.glob, s.key, false) {
				return false
			}
		}
	}
	return true
}

// pathFromSteps converts a replace_paths entry (strings and numbers) to a path.
func pathFromSteps(raw []any) (path, bool) {
	out := make(path, 0, len(raw))
	for _, r := range raw {
		switch v := r.(type) {
		case string:
			out = append(out, step{key: v})
		case float64:
			out = append(out, step{index: int(v), isIdx: true})
		default:
			return nil, false
		}
	}
	return out, true
}
