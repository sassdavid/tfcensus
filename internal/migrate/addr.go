package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	modeManaged = "managed"
	modeData    = "data"
	wildcard    = "*"
)

// instKey is an instance key from count (a number) or for_each (a string).
type instKey struct {
	str   string
	num   int
	isStr bool
}

func (k *instKey) String() string {
	if k == nil {
		return ""
	}
	if k.isStr {
		return `["` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(k.str) + `"]`
	}
	return "[" + strconv.Itoa(k.num) + "]"
}

func (k *instKey) equal(o *instKey) bool {
	if k == nil || o == nil {
		return k == nil && o == nil
	}
	return *k == *o
}

// json returns the key as a state index_key value.
func (k *instKey) json() json.RawMessage {
	if k == nil {
		return nil
	}
	if k.isStr {
		b, _ := json.Marshal(k.str) //nolint:errchkjson // a string always marshals
		return b
	}
	return json.RawMessage(strconv.Itoa(k.num))
}

// eachMode is the state "each" value for instances with this key.
func (k *instKey) eachMode() string {
	switch {
	case k == nil:
		return ""
	case k.isStr:
		return "map"
	default:
		return "list"
	}
}

func keyFromJSON(raw json.RawMessage) (*instKey, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil //nolint:nilnil // no key is a valid result
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return &instKey{str: s, isStr: true}, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("parse index_key %s: %w", raw, err)
	}
	return &instKey{num: n}, nil
}

type moduleStep struct {
	name string
	key  *instKey
}

// Addr is a module, resource or resource instance address.
type Addr struct {
	module []moduleStep
	mode   string // "" for a module address
	typ    string
	name   string
	key    *instKey
}

func (a *Addr) isModule() bool { return a.mode == "" }

func (a *Addr) moduleString() string {
	var b strings.Builder
	for i, m := range a.module {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString("module." + m.name + m.key.String())
	}
	return b.String()
}

// resourceString is the address without the instance key.
func (a *Addr) resourceString() string {
	if a.isModule() {
		return a.moduleString()
	}
	var b strings.Builder
	if len(a.module) > 0 {
		b.WriteString(a.moduleString() + ".")
	}
	if a.mode == modeData {
		b.WriteString("data.")
	}
	b.WriteString(a.typ + "." + a.name)
	return b.String()
}

// String returns the address in terraform's syntax.
func (a *Addr) String() string { return a.resourceString() + a.key.String() }

// modulePrefix reports whether p is a prefix of m. With loose, a step of p without a key matches
// every key.
func modulePrefix(p, m []moduleStep, loose bool) bool {
	if len(p) > len(m) {
		return false
	}
	for i := range p {
		if p[i].name != m[i].name {
			return false
		}
		if loose && p[i].key == nil {
			continue
		}
		if !p[i].key.equal(m[i].key) {
			return false
		}
	}
	return true
}

// contains reports whether instance address in falls under a: in a's module or below it for a
// module address, any instance of a for a resource address, or in itself. loose is as in
// [modulePrefix].
func (a *Addr) contains(in *Addr, loose bool) bool {
	if a.isModule() {
		return modulePrefix(a.module, in.module, loose)
	}
	if !modulePrefix(a.module, in.module, loose) || len(a.module) != len(in.module) {
		return false
	}
	if a.mode != in.mode || a.typ != in.typ || a.name != in.name {
		return false
	}
	return a.key == nil || a.key.equal(in.key)
}

var errAddrSyntax = errors.New("invalid address")

// parseAddr parses a module, resource or instance address.
func parseAddr(s string) (Addr, error) {
	p := addrParser{s: s}
	a, err := p.parse()
	if err != nil {
		return Addr{}, fmt.Errorf("%w %q: %w", errAddrSyntax, s, err)
	}
	return a, nil
}

type addrParser struct {
	s   string
	pos int
}

func (p *addrParser) parse() (Addr, error) {
	var a Addr
	for p.pos < len(p.s) {
		name, err := p.ident()
		if err != nil {
			return Addr{}, err
		}
		switch name {
		case "module":
			if err = p.dot(); err != nil {
				return Addr{}, err
			}
			step := moduleStep{}
			if step.name, err = p.ident(); err != nil {
				return Addr{}, err
			}
			if step.key, err = p.key(); err != nil {
				return Addr{}, err
			}
			a.module = append(a.module, step)
			if p.pos == len(p.s) {
				return a, nil
			}
			if err = p.dot(); err != nil {
				return Addr{}, err
			}
		default:
			return p.resource(a, name)
		}
	}
	return Addr{}, errors.New("empty address")
}

func (p *addrParser) resource(a Addr, first string) (Addr, error) {
	a.mode, a.typ = modeManaged, first
	var err error
	if first == modeData {
		a.mode = modeData
		if err = p.dot(); err != nil {
			return Addr{}, err
		}
		if a.typ, err = p.ident(); err != nil {
			return Addr{}, err
		}
	}
	if err = p.dot(); err != nil {
		return Addr{}, err
	}
	if a.name, err = p.ident(); err != nil {
		return Addr{}, err
	}
	if a.key, err = p.key(); err != nil {
		return Addr{}, err
	}
	if p.pos != len(p.s) {
		return Addr{}, fmt.Errorf("unexpected %q after the resource", p.s[p.pos:])
	}
	return a, nil
}

func (p *addrParser) dot() error {
	if p.pos >= len(p.s) || p.s[p.pos] != '.' {
		return fmt.Errorf("want '.' at offset %d", p.pos)
	}
	p.pos++
	return nil
}

func identChar(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9', c == '-':
		return !first
	}
	return false
}

func (p *addrParser) ident() (string, error) {
	start := p.pos
	for p.pos < len(p.s) && identChar(p.s[p.pos], p.pos == start) {
		p.pos++
	}
	if p.pos == start {
		return "", fmt.Errorf("want a name at offset %d", start)
	}
	return p.s[start:p.pos], nil
}

func (p *addrParser) key() (*instKey, error) {
	if p.pos >= len(p.s) || p.s[p.pos] != '[' {
		return nil, nil //nolint:nilnil // no key is a valid result
	}
	p.pos++
	if p.pos < len(p.s) && p.s[p.pos] == '"' {
		var b strings.Builder
		for p.pos++; p.pos < len(p.s); p.pos++ {
			c := p.s[p.pos]
			if c == '\\' && p.pos+1 < len(p.s) {
				p.pos++
				b.WriteByte(p.s[p.pos])
				continue
			}
			if c == '"' {
				break
			}
			b.WriteByte(c)
		}
		if p.pos+1 >= len(p.s) || p.s[p.pos] != '"' || p.s[p.pos+1] != ']' {
			return nil, errors.New("unterminated string key")
		}
		p.pos += 2
		return &instKey{str: b.String(), isStr: true}, nil
	}
	end := strings.IndexByte(p.s[p.pos:], ']')
	if end < 0 {
		return nil, errors.New("unterminated key")
	}
	n, err := strconv.Atoi(p.s[p.pos : p.pos+end])
	if err != nil {
		return nil, fmt.Errorf("key %q is neither a number nor a quoted string", p.s[p.pos:p.pos+end])
	}
	p.pos += end + 1
	return &instKey{num: n}, nil
}

// wildcardRegexp turns a source with "*" into tfmigrate's pattern: the rest is literal and each
// "*" is a greedy group, so the destination can use $1, $2 and so on.
func wildcardRegexp(src string, anchored bool) (*regexp.Regexp, error) {
	parts := strings.Split(src, wildcard)
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	pattern := strings.Join(parts, "(.*)")
	if anchored {
		pattern = "^" + pattern + "$"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern %q: %w", src, err)
	}
	return re, nil
}

// selector picks instances for a "to" entry: an address, or a pattern with "*" that must match a
// whole instance address.
type selector struct {
	raw  string
	re   *regexp.Regexp
	addr Addr
}

func newSelector(s string) (selector, error) {
	if strings.Contains(s, wildcard) {
		re, err := wildcardRegexp(s, true)
		return selector{raw: s, re: re}, err
	}
	a, err := parseAddr(s)
	return selector{raw: s, addr: a}, err
}

func (s *selector) matches(in *Addr) bool {
	if s.re != nil {
		return s.re.MatchString(in.String())
	}
	return s.addr.contains(in, true)
}

// explicitData reports whether the selector names a data source directly.
func (s *selector) explicitData() bool { return s.re == nil && s.addr.mode == modeData }
