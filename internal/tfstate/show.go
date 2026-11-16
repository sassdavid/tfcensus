package tfstate

import (
	"encoding/json/jsontext"
	"fmt"
	"io"
	"slices"
	"strings"
)

// ParseShow reads the JSON that tofu show -json prints for a state and keeps what [ParseWith]
// keeps. That format has no serial or lineage, and outputs are skipped unread.
func ParseShow(r io.Reader, o ParseOptions) (*State, error) {
	p := parser{d: jsontext.NewDecoder(r), opts: o}
	s := &State{Version: SupportedVersion}
	sp := showParser{p: &p, s: s, at: map[string]int{}}
	err := p.object(func(name string) error {
		var err error
		switch name {
		case "terraform_version":
			s.TerraformVersion, err = p.string()
		case "values":
			err = sp.values()
		default:
			err = p.d.SkipValue()
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("parse show output at byte %d: %w", p.d.InputOffset(), err)
	}
	return s, nil
}

// showParser groups the instances that show lists one by one into resources.
type showParser struct {
	p  *parser
	s  *State
	at map[string]int // resource key to its index in s.Resources
}

func (sp *showParser) values() error {
	p := sp.p
	if p.d.PeekKind() == 'n' {
		return p.d.SkipValue() //nolint:wrapcheck // ParseShow adds the byte offset
	}
	return p.object(func(name string) error {
		if name != "root_module" {
			return p.d.SkipValue() //nolint:wrapcheck // ParseShow adds the byte offset
		}
		return sp.module()
	})
}

func (sp *showParser) module() error {
	p := sp.p
	return p.object(func(name string) error {
		switch name {
		case "resources":
			return p.array(sp.instance)
		case "child_modules":
			return p.array(sp.module)
		default:
			return p.d.SkipValue() //nolint:wrapcheck // ParseShow adds the byte offset
		}
	})
}

// instance reads one entry of a module's resources, which is one resource instance.
func (sp *showParser) instance() error {
	p := sp.p
	var (
		r         Resource
		addr      string
		inst      Instance
		x         extras
		present   = map[string]bool{}
		sensitive []string
	)
	err := p.object(func(name string) error {
		var err error
		switch name {
		case "address":
			addr, err = p.string()
		case "mode":
			var m string
			m, err = p.string()
			r.Mode = Mode(m)
		case stepType:
			r.Type, err = p.string()
			x = p.extras(r.Type)
			x.present = present
		case "name":
			r.Name, err = p.string()
		case "index":
			inst.IndexKey, err = p.indexKey()
		case "provider_name":
			var src string
			src, err = p.string()
			r.Provider.Source, r.Provider.Host = normalizeSource(src)
		case "deposed_key":
			inst.Deposed, err = p.string()
		case "values":
			// tofu writes type before values, so the type's extras are known here.
			err = p.attributes(&inst, x)
		case "sensitive_values":
			sensitive, err = p.sensitiveTree()
		case "identity":
			inst.Identity, err = p.identity()
		default:
			err = p.d.SkipValue()
		}
		return err
	})
	if err != nil {
		return err
	}
	// sensitive_values marks every attribute the schema calls sensitive, set or not. Keep the
	// set ones, as Parse does for sensitive_attributes.
	for _, n := range sensitive {
		if present[n] {
			inst.Sensitive = append(inst.Sensitive, n)
		}
	}
	slices.Sort(inst.Sensitive)
	inst.Sensitive = slices.Compact(inst.Sensitive)
	if x.rules != nil {
		inst.RuleKeys = x.rules.keys(inst.ID)
	}
	r.Module = moduleOf(addr, &r)
	key := r.Module + "\x00" + string(r.Mode) + "\x00" + r.Type + "\x00" + r.Name
	i, ok := sp.at[key]
	if !ok {
		i = len(sp.s.Resources)
		sp.at[key] = i
		sp.s.Resources = append(sp.s.Resources, r)
	}
	sp.s.Resources[i].Instances = append(sp.s.Resources[i].Instances, inst)
	return nil
}

// moduleOf returns the module part of an instance address such as
// module.a["x"].aws_vpc.main[0], which is module.a["x"].
func moduleOf(addr string, r *Resource) string {
	head := r.Type + "." + r.Name
	if r.Mode == ModeData {
		head = "data." + head
	}
	i := strings.LastIndex(addr, "."+head)
	if i < 0 {
		return ""
	}
	if rest := addr[i+1+len(head):]; rest != "" && rest[0] != '[' {
		return ""
	}
	return addr[:i]
}

// sensitiveTree reads sensitive_values, an object that mirrors the attributes with true for
// each sensitive leaf, and returns the top-level names that hold a true. It holds no values.
func (p *parser) sensitiveTree() ([]string, error) {
	if p.d.PeekKind() != '{' {
		return nil, p.d.SkipValue() //nolint:wrapcheck // ParseShow adds the byte offset
	}
	var out []string
	err := p.object(func(name string) error {
		marked, err := p.holdsTrue()
		if marked {
			out = append(out, name)
		}
		return err
	})
	return out, err
}

// holdsTrue reads one value and reports whether it is true or contains a true.
func (p *parser) holdsTrue() (bool, error) {
	switch p.d.PeekKind() { //nolint:exhaustive // only true and containers can mark a value
	case 't':
		_, err := p.d.ReadToken()
		return true, err //nolint:wrapcheck // ParseShow adds the byte offset
	case '{', '[':
	default:
		return false, p.d.SkipValue() //nolint:wrapcheck // ParseShow adds the byte offset
	}
	found, depth := false, 0
	for {
		tok, err := p.d.ReadToken()
		if err != nil {
			return false, err //nolint:wrapcheck // ParseShow adds the byte offset
		}
		switch tok.Kind() { //nolint:exhaustive // other tokens neither nest nor mark
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case 't':
			found = true
		}
		if depth == 0 {
			return found, nil
		}
	}
}
