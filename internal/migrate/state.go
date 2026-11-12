package migrate

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const stateVersion = 4

// ErrStateVersion is returned for a state whose format version tfcensus can't edit.
var ErrStateVersion = errors.New("unsupported state version")

// State is a state file kept as raw JSON: attribute values are never decoded, only moved.
type State struct {
	Version          int             `json:"version"`
	TerraformVersion string          `json:"terraform_version"`
	Serial           uint64          `json:"serial"`
	Lineage          string          `json:"lineage"`
	Outputs          json.RawMessage `json:"outputs"`
	Resources        []*Resource     `json:"resources"`
	CheckResults     json.RawMessage `json:"check_results,omitempty"`
}

// Resource is one resource block of a state. Instances keep every field as raw JSON.
type Resource struct {
	Module    string                       `json:"module,omitempty"`
	Mode      string                       `json:"mode"`
	Type      string                       `json:"type"`
	Name      string                       `json:"name"`
	Each      string                       `json:"each,omitempty"`
	Provider  string                       `json:"provider"`
	Instances []map[string]json.RawMessage `json:"instances"`
}

// ParseState decodes a version 4 state.
func ParseState(b []byte) (*State, error) {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if head.Version != stateVersion {
		return nil, fmt.Errorf("%w %d (want %d)", ErrStateVersion, head.Version, stateVersion)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	s := &State{}
	if err := dec.Decode(s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if len(s.Outputs) == 0 {
		s.Outputs = json.RawMessage("{}")
	}
	return s, nil
}

// newState returns an empty state with a fresh lineage, for a unit that has none yet.
func newState(tfVersion string) (*State, error) {
	lineage, err := newUUID()
	if err != nil {
		return nil, err
	}
	return &State{
		Version: stateVersion, TerraformVersion: tfVersion, Lineage: lineage,
		Outputs: json.RawMessage("{}"), Resources: []*Resource{},
	}, nil
}

// Marshal encodes the state the way terraform writes it, indented by two spaces.
func (s *State) Marshal() ([]byte, error) {
	if s.Resources == nil {
		s.Resources = []*Resource{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("encode state: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *State) clone() *State {
	c := *s
	c.Resources = make([]*Resource, len(s.Resources))
	for i, r := range s.Resources {
		rc := *r
		rc.Instances = slices.Clone(r.Instances)
		c.Resources[i] = &rc
	}
	return &c
}

// addr returns the resource's address without an instance key.
func (r *Resource) addr() (Addr, error) {
	a := Addr{mode: r.Mode, typ: r.Type, name: r.Name}
	if r.Module != "" {
		m, err := parseAddr(r.Module)
		if err != nil {
			return Addr{}, fmt.Errorf("parse module of %s.%s: %w", r.Type, r.Name, err)
		}
		a.module = m.module
	}
	return a, nil
}

// instanceAddr returns the address of the resource's i-th instance.
func (r *Resource) instanceAddr(i int) (Addr, error) {
	a, err := r.addr()
	if err != nil {
		return Addr{}, err
	}
	a.key, err = keyFromJSON(r.Instances[i]["index_key"])
	return a, err
}

// deposed reports whether the instance is a deposed object rather than the current one.
func deposed(inst map[string]json.RawMessage) bool {
	_, ok := inst["deposed"]
	return ok
}

// instances lists every instance address in the state, in state order.
func (s *State) instances() ([]Addr, error) {
	var out []Addr
	for _, r := range s.Resources {
		for i := range r.Instances {
			if deposed(r.Instances[i]) {
				continue
			}
			a, err := r.instanceAddr(i)
			if err != nil {
				return nil, err
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// find returns the resource at a's module, mode, type and name.
func (s *State) find(a *Addr) *Resource {
	want := a.resourceString()
	for _, r := range s.Resources {
		ra, err := r.addr()
		if err == nil && ra.resourceString() == want {
			return r
		}
	}
	return nil
}

// stringAttr returns a top-level string attribute of an instance, such as id or arn. Only
// identifier attributes are read.
func stringAttr(inst map[string]json.RawMessage, name string) string {
	var attrs map[string]json.RawMessage
	if json.Unmarshal(inst["attributes"], &attrs) != nil {
		return ""
	}
	var v string
	if json.Unmarshal(attrs[name], &v) != nil {
		return ""
	}
	return v
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40 //nolint:mnd // version 4
	b[8] = b[8]&0x3f | 0x80 //nolint:mnd // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
