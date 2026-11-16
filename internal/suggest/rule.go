package suggest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// Rule kinds. The syntax is tfautomv's KIND:TYPE:ATTR[:ARG], so its rules work unchanged.
const (
	KindEverything = "everything"
	KindWhitespace = "whitespace"
	KindPrefix     = "prefix"
	KindJSON       = "json"
)

// Parts after the kind: TYPE and ATTR, then PREFIX for KindPrefix.
const (
	ruleParts       = 2
	prefixRuleParts = 3
)

// ErrRule is returned for a rule that doesn't parse.
var ErrRule = errors.New("invalid ignore rule")

// Rule lets two different values of one attribute count as equal.
type Rule struct {
	Kind   string
	Type   string // resource type, or * for every type
	Attr   string // flat attribute key; a * segment matches any one segment, and keys below it match too
	Prefix string // KindPrefix only
}

// ParseRule parses everything:TYPE:ATTR, whitespace:TYPE:ATTR, json:TYPE:ATTR or
// prefix:TYPE:ATTR:PREFIX.
func ParseRule(s string) (Rule, error) {
	kind, rest, _ := strings.Cut(s, ":")
	var r Rule
	switch kind {
	case KindEverything, KindWhitespace, KindJSON:
		parts := strings.Split(rest, ":")
		if len(parts) != ruleParts {
			return Rule{}, fmt.Errorf("%w %q: want %s:TYPE:ATTR", ErrRule, s, kind)
		}
		r = Rule{Kind: kind, Type: parts[0], Attr: parts[1]}
	case KindPrefix:
		parts := strings.SplitN(rest, ":", prefixRuleParts)
		if len(parts) != prefixRuleParts || parts[2] == "" {
			return Rule{}, fmt.Errorf("%w %q: want prefix:TYPE:ATTR:PREFIX", ErrRule, s)
		}
		r = Rule{Kind: kind, Type: parts[0], Attr: parts[1], Prefix: parts[2]}
	default:
		return Rule{}, fmt.Errorf("%w %q: kind must be everything, whitespace, prefix or json", ErrRule, s)
	}
	if r.Type == "" || r.Attr == "" || strings.Contains(r.Attr, "..") ||
		strings.HasPrefix(r.Attr, ".") || strings.HasSuffix(r.Attr, ".") {
		return Rule{}, fmt.Errorf("%w %q: TYPE and ATTR must not be empty", ErrRule, s)
	}
	return r, nil
}

// String returns the rule in the syntax [ParseRule] reads.
func (r Rule) String() string {
	s := r.Kind + ":" + r.Type + ":" + r.Attr
	if r.Kind == KindPrefix {
		s += ":" + r.Prefix
	}
	return s
}

func (r Rule) appliesTo(typ, key string) bool {
	if r.Type != "*" && r.Type != typ {
		return false
	}
	pat := strings.Split(r.Attr, ".")
	segs := strings.Split(key, ".")
	if len(pat) > len(segs) {
		return false
	}
	for i, p := range pat {
		if p != "*" && p != segs[i] {
			return false
		}
	}
	return true
}

// equates reports whether the rule makes a and b equal; nil stands for an absent value.
func (r Rule) equates(a, b any) bool {
	if r.Kind == KindEverything {
		return true
	}
	as, aok := text(a)
	bs, bok := text(b)
	if !aok || !bok {
		return false
	}
	switch r.Kind {
	case KindWhitespace:
		return noSpace(as) == noSpace(bs)
	case KindPrefix:
		return strings.TrimPrefix(as, r.Prefix) == strings.TrimPrefix(bs, r.Prefix)
	case KindJSON:
		return sameJSON(as, bs)
	}
	return false
}

// text returns a string value, with "" for an absent one. Other types don't take part in the text
// rules.
func text(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		return x, true
	}
	return "", false
}

func noSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// sameJSON reports whether a and b decode to equal JSON values. An absent value is JSON null.
func sameJSON(a, b string) bool {
	var av, bv any
	if a == "" {
		a = "null"
	}
	if b == "" {
		b = "null"
	}
	if json.Unmarshal([]byte(a), &av) != nil || json.Unmarshal([]byte(b), &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}
