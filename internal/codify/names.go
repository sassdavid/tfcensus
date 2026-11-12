package codify

import (
	"strconv"
	"strings"
)

// maxName is the longest generated resource name. Terraform has no limit; long names only hurt
// reading plans.
const maxName = 64

// Slug turns s into a TF resource name: lowercase ASCII letters, digits and underscores, starting
// with a letter or underscore. Every other run of characters becomes one underscore.
func Slug(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			under = false
			continue
		}
		if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
	}
	out := strings.TrimRight(b.String(), "_")
	if out == "" {
		return "resource"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "r_" + out
	}
	if len(out) > maxName {
		out = strings.TrimRight(out[:maxName], "_")
	}
	return out
}

// names hands out resource names per TF type, adding _2, _3 and so on to a name that is taken.
type names map[string]map[string]bool

func (n names) take(tfType, name string) {
	if n[tfType] == nil {
		n[tfType] = map[string]bool{}
	}
	n[tfType][name] = true
}

// next returns base, or base with the lowest free suffix, and marks it taken.
func (n names) next(tfType, base string) string {
	name := base
	for i := 2; n[tfType][name]; i++ {
		name = base + "_" + strconv.Itoa(i)
	}
	n.take(tfType, name)
	return name
}

// lastSegment returns the part of an ID or ARN after its last slash or colon.
func lastSegment(s string) string {
	if i := strings.LastIndexAny(s, "/:"); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}
