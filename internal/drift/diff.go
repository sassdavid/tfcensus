package drift

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"unicode/utf8"
)

// maxValueLen caps a value shown with --show-attributes.
const maxValueLen = 80

// differ walks a before and an after value together with their sensitive and unknown markers, which
// mirror the values' structure with true at the marked nodes.
type differ struct {
	show bool
	out  []Attr
}

// diffValues returns one Attr per changed leaf. A sensitive or unknown node is one Attr, and its
// children aren't listed.
func diffValues(before, after, beforeSens, afterSens, afterUnknown any, show bool) []Attr {
	d := &differ{show: show}
	d.walk(nil, before, after, beforeSens, afterSens, afterUnknown)
	return d.out
}

func (d *differ) walk(p path, b, a, bs, as, au any) {
	sens, unk := marked(bs) || marked(as), marked(au)
	if sens || unk {
		if unk || !reflect.DeepEqual(b, a) {
			d.add(p, b, a, sens, unk)
		}
		return
	}
	bm, bIsMap := b.(map[string]any)
	am, aIsMap := a.(map[string]any)
	if bIsMap && aIsMap {
		union := maps.Clone(bm)
		maps.Copy(union, am)
		keys := slices.Sorted(maps.Keys(union))
		for _, k := range keys {
			d.walk(p.child(k), bm[k], am[k], key(bs, k), key(as, k), key(au, k))
		}
		return
	}
	bl, bIsList := b.([]any)
	al, aIsList := a.([]any)
	if bIsList && aIsList {
		for i := range max(len(bl), len(al)) {
			d.walk(p.item(i), at(bl, i), at(al, i), index(bs, i), index(as, i), index(au, i))
		}
		return
	}
	if !reflect.DeepEqual(b, a) {
		d.add(p, b, a, false, false)
	}
}

func (d *differ) add(p path, b, a any, sens, unk bool) {
	attr := Attr{Path: p.String(), Kind: KindChanged, Sensitive: sens, Unknown: unk, segs: p}
	switch {
	case b == nil:
		attr.Kind = KindAdded
	case a == nil && !unk:
		attr.Kind = KindRemoved
	}
	canon, _ := json.Marshal([]any{b, a, unk}) //nolint:errchkjson // decoded JSON always encodes
	attr.value = string(canon)
	if d.show {
		attr.Before, attr.After = shown(b, sens, false), shown(a, sens, unk)
	}
	d.out = append(d.out, attr)
}

func shown(v any, sens, unk bool) *string {
	var s string
	switch {
	case unk:
		s = MaskUnknown
	case sens:
		s = MaskSensitive
	default:
		b, _ := json.Marshal(v) //nolint:errchkjson // decoded JSON always encodes
		s = string(b)
		if utf8.RuneCountInString(s) > maxValueLen {
			s = string([]rune(s)[:maxValueLen]) + "..."
		}
	}
	return &s
}

func marked(m any) bool {
	b, ok := m.(bool)
	return ok && b
}

func key(m any, k string) any {
	switch v := m.(type) {
	case map[string]any:
		return v[k]
	case bool:
		return v
	}
	return nil
}

func index(m any, i int) any {
	switch v := m.(type) {
	case []any:
		return at(v, i)
	case bool:
		return v
	}
	return nil
}

func at(l []any, i int) any {
	if i < len(l) {
		return l[i]
	}
	return nil
}
