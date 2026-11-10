package yamlschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"go.yaml.in/yaml/v4"
)

// Problem is one validation error and where it is.
type Problem struct {
	Origin  string // file path, "env NAME" or "flag --x"
	Line    int    // 0 when there is no position
	Column  int
	Key     string
	Message string
}

func (p Problem) String() string {
	var b strings.Builder
	b.WriteString(p.Origin)
	if p.Line > 0 {
		fmt.Fprintf(&b, ":%d:%d", p.Line, p.Column)
	}
	b.WriteString(": ")
	if p.Key != "" {
		b.WriteString(p.Key)
		b.WriteString(": ")
	}
	b.WriteString(p.Message)
	return b.String()
}

// SortProblems orders problems by line, then column, keeping the order of equal positions.
func SortProblems(problems []Problem) {
	slices.SortStableFunc(problems, func(a, b Problem) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Column - b.Column
	})
}

// Leaf is one schema violation at an instance location.
type Leaf struct {
	Loc     []string
	Message string
	KeyNode bool // report the key's position, not the value's
}

// Schema is a compiled JSON Schema that keeps its raw form for type lookups.
type Schema struct {
	compiled *jsonschema.Schema
	raw      map[string]any
}

// Compile parses and compiles the schema in b, registered under url.
func Compile(url string, b []byte) (*Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("parse schema %s: %w", url, err)
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("add schema %s: %w", url, err)
	}
	compiled, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile schema %s: %w", url, err)
	}
	raw, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema %s is not an object", url)
	}
	return &Schema{compiled: compiled, raw: raw}, nil
}

// Raw returns the schema document as decoded JSON.
func (s *Schema) Raw() map[string]any { return s.raw }

// TypeAt returns the "type" of the property at path, following nested "properties", or "".
func (s *Schema) TypeAt(path []string) string {
	node := s.raw
	for _, p := range path {
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return ""
		}
		next, ok := props[p].(map[string]any)
		if !ok {
			return ""
		}
		node = next
	}
	t, _ := node["type"].(string)
	return t
}

// Validate checks a JSON value (see [ToJSONValue]) and returns its violations sorted by location.
func (s *Schema) Validate(inst any) ([]Leaf, error) {
	err := s.compiled.Validate(inst)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, fmt.Errorf("validate: %w", err)
	}
	var out []Leaf
	collectLeaves(ve, &out)
	slices.SortStableFunc(out, func(a, b Leaf) int {
		return strings.Compare(strings.Join(a.Loc, "/"), strings.Join(b.Loc, "/"))
	})
	return out, nil
}

func collectLeaves(ve *jsonschema.ValidationError, out *[]Leaf) {
	if len(ve.Causes) > 0 {
		for _, c := range ve.Causes {
			collectLeaves(c, out)
		}
		return
	}
	switch k := ve.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		for _, p := range k.Properties {
			*out = append(*out, Leaf{
				Loc:     append(slices.Clone(ve.InstanceLocation), p),
				Message: "unknown key",
				KeyNode: true,
			})
		}
	case *kind.Type:
		*out = append(*out, Leaf{
			Loc:     ve.InstanceLocation,
			Message: fmt.Sprintf("wrong type: got %s, want %s", k.Got, strings.Join(k.Want, " or ")),
		})
	default:
		*out = append(*out, Leaf{Loc: ve.InstanceLocation, Message: leafMessage(ve)})
	}
}

func leafMessage(ve *jsonschema.ValidationError) string {
	leaf := &jsonschema.ValidationError{ErrorKind: ve.ErrorKind}
	// Error() renders "at '': <message>" for a location-less error.
	_, msg, ok := strings.Cut(leaf.Error(), ": ")
	if !ok {
		return leaf.Error()
	}
	return msg
}

// Document is a parsed YAML file.
type Document struct {
	Data any        // decoded value; an empty file decodes to an empty map
	Root *yaml.Node // for positions, see [NodeAt]
}

// Parse decodes the YAML in b and validates it. Syntax errors and schema violations come back as
// problems with origin and position; the error is only for failures of the validator itself.
func (s *Schema) Parse(origin string, b []byte) (*Document, []Problem, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, []Problem{syntaxProblem(origin, err)}, nil
	}
	var data any
	if err := root.Decode(&data); err != nil {
		return nil, []Problem{syntaxProblem(origin, err)}, nil
	}
	if data == nil { // empty or comment-only file
		data = map[string]any{}
	}
	inst, err := ToJSONValue(data)
	if err != nil {
		//nolint:nilerr // a value JSON can't hold is a problem in the file, not a validator failure
		return nil, []Problem{{Origin: origin, Message: err.Error()}}, nil
	}
	leaves, err := s.Validate(inst)
	if err != nil {
		return nil, nil, err
	}
	if len(leaves) == 0 {
		return &Document{Data: data, Root: &root}, nil, nil
	}
	problems := make([]Problem, 0, len(leaves))
	for _, l := range leaves {
		p := Problem{Origin: origin, Key: strings.Join(l.Loc, "."), Message: l.Message}
		if n := NodeAt(&root, l.Loc, l.KeyNode); n != nil {
			p.Line, p.Column = n.Line, n.Column
		}
		problems = append(problems, p)
	}
	SortProblems(problems)
	return nil, problems, nil
}

func syntaxProblem(origin string, err error) Problem {
	// yaml errors already carry "line N:" positions.
	return Problem{Origin: origin, Message: "invalid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
}

// ToJSONValue converts a decoded YAML value into the form the validator expects.
func ToJSONValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("unsupported YAML value (only string keys are allowed): %w", err)
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("convert YAML to JSON: %w", err)
	}
	return out, nil
}

// NodeAt returns the YAML node at loc, or the deepest node that exists. With keyNode, a mapping
// entry's key node is returned instead of its value.
func NodeAt(root *yaml.Node, loc []string, keyNode bool) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for i, seg := range loc {
		last := i == len(loc)-1
		switch n.Kind { //nolint:exhaustive // only containers can be descended into
		case yaml.MappingNode:
			k, v := mappingEntry(n, seg)
			if k == nil {
				return n
			}
			if last && keyNode {
				return k
			}
			n = v
		case yaml.SequenceNode:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(n.Content) {
				return n
			}
			n = n.Content[idx]
		default:
			return n
		}
	}
	return n
}

func mappingEntry(n *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}
