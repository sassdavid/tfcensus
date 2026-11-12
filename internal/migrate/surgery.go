package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// ErrConflict is returned when a move would overwrite an object that already exists.
var ErrConflict = errors.New("address already exists")

// ErrNoMatch is returned when an address or pattern matches nothing in the state.
var ErrNoMatch = errors.New("no matching objects")

// ErrDataSource is returned when a move names a data source; data sources are read again, not moved.
var ErrDataSource = errors.New("data sources can't be moved")

// taken is the outcome of [State.take].
type taken struct {
	resources []*Resource
	moved     []string // instance addresses
	skipped   []string // data source instances left in the source
}

// take removes the managed instances that sels match from s and returns them with their
// resources. Data sources a selector matches through a module or pattern stay in s. Each selector
// must match something.
func (s *State) take(sels []selector) (*taken, error) {
	out := &taken{}
	used := make([]bool, len(sels))
	for _, sel := range sels {
		if sel.explicitData() {
			return nil, fmt.Errorf("%w: %s", ErrDataSource, sel.raw)
		}
	}
	kept := s.Resources[:0]
	for _, r := range s.Resources {
		moving, staying, err := out.split(r, sels, used)
		if err != nil {
			return nil, err
		}
		if len(moving) > 0 {
			mr := *r
			mr.Instances = moving
			out.resources = append(out.resources, &mr)
		}
		if len(staying) > 0 {
			r.Instances = staying
			kept = append(kept, r)
		}
	}
	s.Resources = kept
	for j, ok := range used {
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNoMatch, sels[j].raw)
		}
	}
	return out, nil
}

// split sorts r's instances into those that move and those that stay, marking the selectors
// that matched in used.
func (t *taken) split(
	r *Resource,
	sels []selector,
	used []bool,
) ([]map[string]json.RawMessage, []map[string]json.RawMessage, error) {
	var moving, staying []map[string]json.RawMessage
	for i := range r.Instances {
		a, err := r.instanceAddr(i)
		if err != nil {
			return nil, nil, err
		}
		hit := false
		for j := range sels {
			if sels[j].matches(&a) {
				used[j], hit = true, true
			}
		}
		switch {
		case !hit:
			staying = append(staying, r.Instances[i])
		case r.Mode == modeData:
			staying = append(staying, r.Instances[i])
			t.skipped = appendOnce(t.skipped, a.String())
		default:
			moving = append(moving, r.Instances[i])
			if !deposed(r.Instances[i]) {
				t.moved = append(t.moved, a.String())
			}
		}
	}
	return moving, staying, nil
}

func appendOnce(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

// hasKey reports whether r has a current instance with key k.
func (r *Resource) hasKey(k *instKey) (bool, error) {
	for i := range r.Instances {
		a, err := r.instanceAddr(i)
		if err != nil {
			return false, err
		}
		if a.key.equal(k) && !deposed(r.Instances[i]) {
			return true, nil
		}
	}
	return false, nil
}

// canHold fails when r can't take an instance with key k: its instances are keyed differently,
// or k is taken.
func (r *Resource) canHold(k *instKey, label string) error {
	if r.Each != k.eachMode() {
		return fmt.Errorf("%w: %s holds instances keyed differently", ErrConflict, label)
	}
	taken, err := r.hasKey(k)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("%w: %s", ErrConflict, label+k.String())
	}
	return nil
}

// put adds moved resources to s, merging instances into a resource that already exists. It fails
// when an instance with the same key is already there.
func (s *State) put(rs []*Resource) error {
	for _, r := range rs {
		ra, err := r.addr()
		if err != nil {
			return err
		}
		existing := s.find(&ra)
		if existing == nil {
			s.Resources = append(s.Resources, r)
			continue
		}
		for i := range r.Instances {
			k, keyErr := keyFromJSON(r.Instances[i]["index_key"])
			if keyErr != nil {
				return keyErr
			}
			if keyErr = existing.canHold(k, ra.resourceString()); keyErr != nil {
				return keyErr
			}
			existing.Instances = append(existing.Instances, r.Instances[i])
		}
	}
	return nil
}

// providerRe splits a provider configuration address into its module part, provider source and
// alias.
var providerRe = regexp.MustCompile(`^(?:(.*)\.)?provider\["([^"]+)"\](?:\.([A-Za-z0-9_-]+))?$`)

// providerSource returns the source address inside a provider configuration address.
func providerSource(p string) string {
	if m := providerRe.FindStringSubmatch(p); m != nil {
		return m[2]
	}
	return ""
}

// rewriteProviders points moved resources at the destination's provider configuration: the one
// the destination state already uses for that provider when there is exactly one, else the
// default root configuration. It returns the rewrites as "old -> new".
func rewriteProviders(rs []*Resource, dst *State) []string {
	used := map[string][]string{}
	for _, r := range dst.Resources {
		src := providerSource(r.Provider)
		if src != "" && !slices.Contains(used[src], r.Provider) {
			used[src] = append(used[src], r.Provider)
		}
	}
	var notes []string
	for _, r := range rs {
		src := providerSource(r.Provider)
		if src == "" {
			continue
		}
		want := `provider["` + src + `"]`
		switch cands := used[src]; {
		case slices.Contains(cands, r.Provider):
			want = r.Provider
		case len(cands) == 1:
			want = cands[0]
		}
		if want != r.Provider {
			notes = appendOnce(notes, r.Provider+" -> "+want)
			r.Provider = want
		}
	}
	return notes
}

// action is one parsed tfmigrate state action.
type action struct {
	verb string
	args []string
}

const (
	verbMv  = "mv"
	verbXmv = "xmv"
	verbRm  = "rm"
)

func parseAction(s string) (action, error) {
	words, err := splitWords(s)
	if err != nil {
		return action{}, fmt.Errorf("parse action %q: %w", s, err)
	}
	if len(words) == 0 {
		return action{}, errors.New("empty action")
	}
	a := action{verb: words[0], args: words[1:]}
	switch a.verb {
	case verbMv, verbXmv:
		if len(a.args) != 2 { //nolint:mnd // source and destination
			return action{}, fmt.Errorf("action %q: %s takes a source and a destination", s, a.verb)
		}
	case verbRm:
		if len(a.args) == 0 {
			return action{}, fmt.Errorf("action %q: rm takes at least one address", s)
		}
	case "import", "replace-provider":
		return action{}, fmt.Errorf("action %q: %s is not supported; use the native strategy for imports", s, a.verb)
	default:
		return action{}, fmt.Errorf("action %q: unknown verb %q (want mv, xmv or rm)", s, a.verb)
	}
	return a, nil
}

// apply runs the action on s and returns the addresses it touched, as "src -> dst" for moves.
func (a action) apply(s *State) ([]string, error) {
	switch a.verb {
	case verbMv:
		return s.mv(a.args[0], a.args[1])
	case verbXmv:
		return s.xmv(a.args[0], a.args[1])
	default:
		return s.rm(a.args)
	}
}

// xmv expands a source with "*" against every instance address the way tfmigrate does (the first
// unanchored match in each address becomes a source, $1 and so on fill the destination) and moves
// each match.
func (s *State) xmv(src, dst string) ([]string, error) {
	if !strings.Contains(src, wildcard) {
		return s.mv(src, dst)
	}
	re, err := wildcardRegexp(src, false)
	if err != nil {
		return nil, err
	}
	addrs, err := s.instances()
	if err != nil {
		return nil, err
	}
	var sources []string
	for i := range addrs {
		if m := re.FindString(addrs[i].String()); m != "" {
			sources = appendOnce(sources, m)
		}
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoMatch, src)
	}
	var out []string
	for _, m := range sources {
		done, mvErr := s.mv(m, re.ReplaceAllString(m, dst))
		if mvErr != nil {
			return nil, mvErr
		}
		out = append(out, done...)
	}
	return out, nil
}

// mv moves a module, a whole resource or one instance within s, as terraform state mv does.
func (s *State) mv(srcS, dstS string) ([]string, error) {
	src, err := parseAddr(srcS)
	if err != nil {
		return nil, err
	}
	dst, err := parseAddr(dstS)
	if err != nil {
		return nil, err
	}
	switch {
	case src.isModule() != dst.isModule():
		return nil, fmt.Errorf("mv %s %s: can't move between a module and a resource", srcS, dstS)
	case src.isModule():
		return s.mvModule(&src, &dst)
	case src.mode != dst.mode || src.typ != dst.typ:
		return nil, fmt.Errorf("mv %s %s: the resource type and mode must stay the same", srcS, dstS)
	case src.key == nil && dst.key == nil:
		return s.mvResource(&src, &dst)
	default:
		return s.mvInstance(&src, &dst)
	}
}

func (s *State) mvModule(src, dst *Addr) ([]string, error) {
	var hits []*Resource
	for _, r := range s.Resources {
		ra, err := r.addr()
		if err != nil {
			return nil, err
		}
		if modulePrefix(src.module, ra.module, false) {
			hits = append(hits, r)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoMatch, src.String())
	}
	for _, r := range hits {
		ra, _ := r.addr()
		na := ra
		na.module = append(slices.Clone(dst.module), ra.module[len(src.module):]...)
		if other := s.find(&na); other != nil {
			return nil, fmt.Errorf("%w: %s", ErrConflict, na.String())
		}
		r.Module = na.moduleString()
	}
	return []string{src.String() + " -> " + dst.String()}, nil
}

func (s *State) mvResource(src, dst *Addr) ([]string, error) {
	r := s.find(src)
	if r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoMatch, src.String())
	}
	if s.find(dst) != nil {
		return nil, fmt.Errorf("%w: %s", ErrConflict, dst.String())
	}
	r.Module, r.Name = dst.moduleString(), dst.name
	return []string{src.String() + " -> " + dst.String()}, nil
}

func (r *Resource) instanceIndex(k *instKey) (int, error) {
	for i := range r.Instances {
		a, err := r.instanceAddr(i)
		if err != nil {
			return -1, err
		}
		if a.key.equal(k) && !deposed(r.Instances[i]) {
			return i, nil
		}
	}
	return -1, nil
}

func (s *State) mvInstance(src, dst *Addr) ([]string, error) {
	r := s.find(src)
	if r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoMatch, src.String())
	}
	idx, err := r.instanceIndex(src.key)
	if err != nil {
		return nil, err
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoMatch, src.String())
	}
	inst := maps.Clone(r.Instances[idx])
	if k := dst.key.json(); k != nil {
		inst["index_key"] = k
	} else {
		delete(inst, "index_key")
	}
	moved := &Resource{
		Module: dst.moduleString(), Mode: r.Mode, Type: r.Type, Name: dst.name, Each: dst.key.eachMode(),
		Provider: r.Provider, Instances: []map[string]json.RawMessage{inst},
	}
	// Check before removing, so a conflict leaves the state as it was.
	if other := s.find(dst); other != nil && other != r {
		if err = other.canHold(dst.key, dst.resourceString()); err != nil {
			return nil, err
		}
	}
	r.Instances = slices.Delete(r.Instances, idx, idx+1)
	if len(r.Instances) == 0 {
		s.Resources = slices.DeleteFunc(s.Resources, func(x *Resource) bool { return x == r })
	}
	if err = s.put([]*Resource{moved}); err != nil {
		return nil, err
	}
	return []string{src.String() + " -> " + dst.String()}, nil
}

// rm removes every object under each address, as terraform state rm does.
func (s *State) rm(addrs []string) ([]string, error) {
	var out []string
	for _, raw := range addrs {
		a, err := parseAddr(raw)
		if err != nil {
			return nil, err
		}
		removed, err := s.removeUnder(&a)
		if err != nil {
			return nil, err
		}
		if !removed {
			return nil, fmt.Errorf("%w: %s", ErrNoMatch, raw)
		}
		out = append(out, a.String())
	}
	return out, nil
}

func (s *State) removeUnder(a *Addr) (bool, error) {
	removed := false
	kept := s.Resources[:0]
	for _, r := range s.Resources {
		var stay []map[string]json.RawMessage
		for i := range r.Instances {
			ia, err := r.instanceAddr(i)
			if err != nil {
				return false, err
			}
			if a.contains(&ia, false) {
				removed = true
				continue
			}
			stay = append(stay, r.Instances[i])
		}
		if len(stay) > 0 {
			r.Instances = stay
			kept = append(kept, r)
		}
	}
	s.Resources = kept
	return removed, nil
}
