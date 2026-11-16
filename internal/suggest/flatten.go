package suggest

import (
	"strconv"
	"strings"
)

// flatten turns a resource value into flat keys: object attributes and map keys join with ".",
// list elements use their index, and a non-empty list adds "<key>.#" with its length. Null values,
// empty strings and empty collections add nothing, so null equals empty.
func flatten(prefix string, v any, out map[string]any) {
	switch x := v.(type) {
	case nil:
	case map[string]any:
		for k, e := range x {
			flatten(join(prefix, k), e, out)
		}
	case []any:
		if len(x) == 0 {
			return
		}
		out[join(prefix, "#")] = float64(len(x))
		for i, e := range x {
			flatten(join(prefix, strconv.Itoa(i)), e, out)
		}
	case string:
		if x != "" {
			out[prefix] = x
		}
	default:
		out[prefix] = x
	}
}

// marks collects the keys that after_unknown sets to true. A key covers every key below it.
func marks(prefix string, v any, out map[string]bool) {
	switch x := v.(type) {
	case bool:
		if x && prefix != "" {
			out[prefix] = true
		}
	case map[string]any:
		for k, e := range x {
			marks(join(prefix, k), e, out)
		}
	case []any:
		for i, e := range x {
			marks(join(prefix, strconv.Itoa(i)), e, out)
		}
	}
}

func join(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// covered reports whether key or one of its ancestors is in set.
func covered(set map[string]bool, key string) bool {
	if len(set) == 0 {
		return false
	}
	for {
		if set[key] {
			return true
		}
		i := strings.LastIndexByte(key, '.')
		if i < 0 {
			return false
		}
		key = key[:i]
	}
}

// topLevel returns the attribute name a flat key starts with.
func topLevel(key string) string {
	name, _, _ := strings.Cut(key, ".")
	return name
}
