package tfstate

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// SupportedVersion is the only raw state format version tfcensus reads.
const SupportedVersion = 4

// Mode says whether a resource is managed or a data source.
type Mode string

// Resource modes.
const (
	ModeManaged Mode = "managed"
	ModeData    Mode = "data"
)

// ErrUnsupportedVersion is returned for a state whose format is older or newer than version 4.
var ErrUnsupportedVersion = errors.New("unsupported state format version")

// State is the part of a raw state file tfcensus keeps: metadata and resource identifiers. It
// never holds other attribute values.
type State struct {
	Version          int
	TerraformVersion string
	Serial           uint64
	Lineage          string
	Encrypted        bool // an OpenTofu client-side encrypted payload; Resources is empty
	Resources        []Resource
}

// Resource is one resource block in state.
type Resource struct {
	Mode      Mode
	Type      string
	Name      string
	Module    string // module path such as module.vpc, empty for the root module
	Provider  Provider
	Instances []Instance
}

// Instance holds the identifiers of one resource instance.
type Instance struct {
	IndexKey string            // count index or for_each key in address syntax: 0 or "a"
	Deposed  string            // the deposed object key; empty for the current object
	ID       string            // the id attribute
	ARN      string            // the arn attribute
	Region   string            // the region attribute (aws provider 6 and later)
	Identity map[string]string // resource identity (Terraform 1.12 and later), scalars only
}

// Address returns the instance's resource address, such as module.vpc.aws_subnet.private[0].
func (r *Resource) Address(i *Instance) string {
	var b strings.Builder
	if r.Module != "" {
		b.WriteString(r.Module + ".")
	}
	if r.Mode == ModeData {
		b.WriteString("data.")
	}
	b.WriteString(r.Type + "." + r.Name)
	if i != nil && i.IndexKey != "" {
		b.WriteString("[" + i.IndexKey + "]")
	}
	return b.String()
}

// Managed returns the managed resources.
func (s *State) Managed() []Resource {
	var out []Resource
	for _, r := range s.Resources {
		if r.Mode == ModeManaged {
			out = append(out, r)
		}
	}
	return out
}

// The only attributes the parser keeps.
const (
	attrID     = "id"
	attrARN    = "arn"
	attrRegion = "region"
)

func identifierAttrs() []string { return []string{attrID, attrARN, attrRegion} }

// Parse reads raw state JSON from r. It streams: attribute values other than id, arn and region
// are skipped without being stored. An encrypted payload returns a State with Encrypted set and
// a nil error; a state in another format version returns [ErrUnsupportedVersion] and the
// metadata read so far.
func Parse(r io.Reader) (*State, error) {
	p := parser{d: jsontext.NewDecoder(r)}
	s, err := p.state()
	if err != nil {
		return nil, fmt.Errorf("parse state at byte %d: %w", p.d.InputOffset(), err)
	}
	if !s.Encrypted && s.Version != SupportedVersion {
		return s, fmt.Errorf("%w %d: tfcensus reads version %d (Terraform 0.12 and later)",
			ErrUnsupportedVersion, s.Version, SupportedVersion)
	}
	return s, nil
}

type parser struct {
	d *jsontext.Decoder
}

func (p *parser) state() (*State, error) {
	s := &State{}
	err := p.object(func(name string) error {
		var err error
		switch name {
		case "version":
			s.Version, err = p.int()
		case "terraform_version":
			s.TerraformVersion, err = p.string()
		case "serial":
			s.Serial, err = p.uint()
		case "lineage":
			s.Lineage, err = p.string()
		case "resources":
			s.Resources, err = p.resources()
		case "encrypted_data", "encryption_version":
			s.Encrypted = true
			err = p.d.SkipValue()
		default:
			err = p.d.SkipValue()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.Encrypted {
		s.Resources = nil
	}
	return s, nil
}

// object reads an object and calls field for each member name, with the decoder positioned at
// the member's value. field must consume the value.
func (p *parser) object(field func(name string) error) error {
	if err := p.expect('{'); err != nil {
		return err
	}
	for p.d.PeekKind() != '}' {
		tok, err := p.d.ReadToken()
		if err != nil {
			return err //nolint:wrapcheck // Parse adds the byte offset
		}
		if err = field(tok.String()); err != nil {
			return err
		}
	}
	return p.expect('}')
}

// array reads an array and calls elem with the decoder positioned at each element.
func (p *parser) array(elem func() error) error {
	if p.d.PeekKind() == 'n' {
		return p.d.SkipValue() //nolint:wrapcheck // Parse adds the byte offset
	}
	if err := p.expect('['); err != nil {
		return err
	}
	for p.d.PeekKind() != ']' {
		if err := elem(); err != nil {
			return err
		}
	}
	return p.expect(']')
}

func (p *parser) expect(k jsontext.Kind) error {
	tok, err := p.d.ReadToken()
	if err != nil {
		return err //nolint:wrapcheck // Parse adds the byte offset
	}
	if tok.Kind() != k {
		return fmt.Errorf("want %v, got %v", k, tok.Kind())
	}
	return nil
}

func (p *parser) string() (string, error) {
	tok, err := p.d.ReadToken()
	if err != nil {
		return "", err //nolint:wrapcheck // Parse adds the byte offset
	}
	if k := tok.Kind(); k == '"' || k == 'n' {
		return tok.String(), nil
	}
	return "", fmt.Errorf("want a string, got %v", tok.Kind())
}

func (p *parser) uint() (uint64, error) {
	tok, err := p.d.ReadToken()
	if err != nil {
		return 0, err //nolint:wrapcheck // Parse adds the byte offset
	}
	if tok.Kind() != '0' {
		return 0, fmt.Errorf("want a number, got %v", tok.Kind())
	}
	n, err := tok.Uint()
	if err != nil {
		return 0, fmt.Errorf("read number: %w", err)
	}
	return n, nil
}

func (p *parser) int() (int, error) {
	n, err := p.uint()
	if err != nil {
		return 0, err
	}
	if n > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("number %d out of range", n)
	}
	return int(n), nil
}

func (p *parser) resources() ([]Resource, error) {
	var out []Resource
	err := p.array(func() error {
		r, err := p.resource()
		if err != nil {
			return err
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

func (p *parser) resource() (Resource, error) {
	var r Resource
	err := p.object(func(name string) error {
		var err error
		switch name {
		case "mode":
			var m string
			m, err = p.string()
			r.Mode = Mode(m)
		case "type":
			r.Type, err = p.string()
		case "name":
			r.Name, err = p.string()
		case "module":
			r.Module, err = p.string()
		case "provider":
			var raw string
			raw, err = p.string()
			r.Provider = ParseProvider(raw)
		case "instances":
			err = p.array(func() error {
				inst, instErr := p.instance()
				if instErr != nil {
					return instErr
				}
				r.Instances = append(r.Instances, inst)
				return nil
			})
		default:
			err = p.d.SkipValue()
		}
		return err
	})
	return r, err
}

func (p *parser) instance() (Instance, error) {
	var inst Instance
	err := p.object(func(name string) error {
		var err error
		switch name {
		case "index_key":
			inst.IndexKey, err = p.indexKey()
		case "deposed":
			inst.Deposed, err = p.string()
		case "attributes":
			err = p.attributes(&inst)
		case "attributes_flat":
			err = p.flatAttributes(&inst)
		case "identity":
			inst.Identity, err = p.identity()
		default:
			err = p.d.SkipValue()
		}
		return err
	})
	return inst, err
}

func (p *parser) indexKey() (string, error) {
	tok, err := p.d.ReadToken()
	if err != nil {
		return "", err //nolint:wrapcheck // Parse adds the byte offset
	}
	k := tok.Kind()
	if k == '0' {
		return tok.String(), nil
	}
	if k == '"' {
		return strconv.Quote(tok.String()), nil
	}
	if k == 'n' {
		return "", nil
	}
	return "", fmt.Errorf("want a number or string index key, got %v", tok.Kind())
}

// attributes keeps the identifier attributes and skips every other value unread.
func (p *parser) attributes(inst *Instance) error {
	if p.d.PeekKind() == 'n' {
		return p.d.SkipValue() //nolint:wrapcheck // Parse adds the byte offset
	}
	return p.object(func(name string) error {
		if !slices.Contains(identifierAttrs(), name) || p.d.PeekKind() != '"' {
			return p.d.SkipValue() //nolint:wrapcheck // Parse adds the byte offset
		}
		v, err := p.string()
		inst.set(name, v)
		return err
	})
}

// flatAttributes reads the flatmap form that states upgraded from format 3 can still carry.
func (p *parser) flatAttributes(inst *Instance) error {
	if p.d.PeekKind() == 'n' {
		return p.d.SkipValue() //nolint:wrapcheck // Parse adds the byte offset
	}
	return p.object(func(name string) error {
		if !slices.Contains(identifierAttrs(), name) || p.d.PeekKind() != '"' {
			return p.d.SkipValue() //nolint:wrapcheck // Parse adds the byte offset
		}
		v, err := p.string()
		if inst.get(name) == "" {
			inst.set(name, v)
		}
		return err
	})
}

// identity keeps the scalar members of a resource identity. Identity attributes are identifiers
// by definition, so their values are safe to keep.
func (p *parser) identity() (map[string]string, error) {
	if p.d.PeekKind() == 'n' {
		return nil, p.d.SkipValue() //nolint:wrapcheck // Parse adds the byte offset
	}
	out := map[string]string{}
	err := p.object(func(name string) error {
		if k := p.d.PeekKind(); k != '"' && k != '0' && k != 't' && k != 'f' {
			return p.d.SkipValue() //nolint:wrapcheck // Parse adds the byte offset
		}
		tok, err := p.d.ReadToken()
		if err != nil {
			return err //nolint:wrapcheck // Parse adds the byte offset
		}
		out[name] = tok.String()
		return nil
	})
	if len(out) == 0 {
		return nil, err
	}
	return out, err
}

func (i *Instance) set(name, v string) {
	switch name {
	case attrID:
		i.ID = v
	case attrARN:
		i.ARN = v
	case attrRegion:
		i.Region = v
	}
}

func (i *Instance) get(name string) string {
	switch name {
	case attrID:
		return i.ID
	case attrARN:
		return i.ARN
	case attrRegion:
		return i.Region
	}
	return ""
}

// IdentityString returns the identity as sorted key=value pairs, or "" without an identity.
func (i *Instance) IdentityString() string {
	if len(i.Identity) == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(i.Identity))
	parts := make([]string, len(keys))
	for n, k := range keys {
		parts[n] = k + "=" + i.Identity[k]
	}
	return strings.Join(parts, ",")
}
