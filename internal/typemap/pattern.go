package typemap

import (
	"fmt"
	"regexp"
	"strings"
)

// Fields that every ARN pattern and template may use.
const (
	FieldPartition = "partition"
	FieldRegion    = "region"
	FieldAccount   = "account"
	FieldID        = "id"
	FieldARN       = "arn"
)

// Pattern is a compiled ARN pattern such as arn:{partition}:ec2:{region}:{account}:vpc/{id}.
// {name} matches one ARN segment (no slash or colon), {name+} matches the rest, and
// {name:regexp} matches regexp.
type Pattern struct {
	src string
	re  *regexp.Regexp
}

var placeholderRE = regexp.MustCompile(`\{([a-z_]+)(\+|:[^}]+)?\}`)

// CompilePattern compiles an ARN pattern.
func CompilePattern(src string) (*Pattern, error) {
	var b strings.Builder
	b.WriteString("^")
	last := 0
	for _, m := range placeholderRE.FindAllStringSubmatchIndex(src, -1) {
		b.WriteString(regexp.QuoteMeta(src[last:m[0]]))
		name := src[m[2]:m[3]]
		mod := ""
		if m[4] >= 0 {
			mod = src[m[4]:m[5]]
		}
		expr := `[^/:]+`
		switch {
		case name == FieldPartition:
			expr = `[^:]+`
		case name == FieldRegion || name == FieldAccount:
			expr = `[^:]*`
		case mod == "+":
			expr = `.+`
		case strings.HasPrefix(mod, ":"):
			expr = mod[1:]
		}
		fmt.Fprintf(&b, "(?P<%s>%s)", name, expr)
		last = m[1]
	}
	b.WriteString(regexp.QuoteMeta(src[last:]))
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("compile arn pattern %q: %w", src, err)
	}
	return &Pattern{src: src, re: re}, nil
}

// String returns the pattern's source.
func (p *Pattern) String() string { return p.src }

// Match returns the placeholder values in arn, or nil when arn doesn't match.
func (p *Pattern) Match(arn string) map[string]string {
	m := p.re.FindStringSubmatch(arn)
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for i, name := range p.re.SubexpNames() {
		if name != "" {
			out[name] = m[i]
		}
	}
	return out
}

// Template renders an ID template such as {zone_id}_{name}_{type}[_{set_identifier}]. A part in
// brackets is left out when one of its fields is empty.
type Template string

// Render fills t from lookup. It reports false when a field outside brackets is empty.
func (t Template) Render(lookup func(string) string) (string, bool) {
	var b strings.Builder
	s := string(t)
	for s != "" {
		open := strings.IndexByte(s, '[')
		if open < 0 {
			part, ok := fill(s, lookup)
			if !ok {
				return "", false
			}
			b.WriteString(part)
			break
		}
		part, ok := fill(s[:open], lookup)
		if !ok {
			return "", false
		}
		b.WriteString(part)
		end := strings.IndexByte(s[open:], ']')
		if end < 0 {
			return "", false
		}
		if opt, filled := fill(s[open+1:open+end], lookup); filled {
			b.WriteString(opt)
		}
		s = s[open+end+1:]
	}
	return b.String(), true
}

func fill(s string, lookup func(string) string) (string, bool) {
	ok := true
	out := placeholderRE.ReplaceAllStringFunc(s, func(m string) string {
		v := lookup(placeholderRE.FindStringSubmatch(m)[1])
		if v == "" {
			ok = false
		}
		return v
	})
	return out, ok
}

// Fields returns the field names t uses.
func (t Template) Fields() []string {
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(string(t), -1) {
		out = append(out, m[1])
	}
	return out
}

// NormalizeARN returns arn in the form state stores it: CloudWatch Logs reports log group ARNs
// with a trailing :*, the aws provider stores them without.
func NormalizeARN(arn string) string {
	if strings.Contains(arn, ":log-group:") {
		return strings.TrimSuffix(arn, ":*")
	}
	return arn
}
