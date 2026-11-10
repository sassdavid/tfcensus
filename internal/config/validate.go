package config

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

	"github.com/sassdavid/tfcensus/schemas"
)

// ErrInvalid is wrapped by every configuration error.
var ErrInvalid = errors.New("invalid configuration")

// Problem is one configuration error and where it is.
type Problem struct {
	Origin  string // file path, "env TFCENSUS_X" or "flag --x"
	Line    int    // 0 for env vars and flags
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

// ValidationError lists every [Problem] found. It unwraps to [ErrInvalid].
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}

func (*ValidationError) Unwrap() error { return ErrInvalid }

// schemaDoc keeps the raw schema next to the compiled one to look up key types.
type schemaDoc struct {
	compiled *jsonschema.Schema
	raw      map[string]any
}

const (
	typeArray  = "array"
	typeString = "string"
)

const configSchemaURL = "https://github.com/sassdavid/tfcensus/schemas/" + schemas.ConfigV1

func loadSchema() (*schemaDoc, error) {
	b, err := schemas.Read(schemas.ConfigV1)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("parse config schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource(configSchemaURL, doc); err != nil {
		return nil, fmt.Errorf("add config schema: %w", err)
	}
	compiled, err := c.Compile(configSchemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile config schema: %w", err)
	}
	raw, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("config schema is not an object")
	}
	return &schemaDoc{compiled: compiled, raw: raw}, nil
}

type leafError struct {
	loc     []string
	message string
	keyNode bool // report the key's position, not the value's
}

func (s *schemaDoc) validate(inst any) ([]leafError, error) {
	err := s.compiled.Validate(inst)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	var out []leafError
	collectLeaves(ve, &out)
	slices.SortStableFunc(out, func(a, b leafError) int {
		return strings.Compare(strings.Join(a.loc, "/"), strings.Join(b.loc, "/"))
	})
	return out, nil
}

func collectLeaves(ve *jsonschema.ValidationError, out *[]leafError) {
	if len(ve.Causes) > 0 {
		for _, c := range ve.Causes {
			collectLeaves(c, out)
		}
		return
	}
	switch k := ve.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		for _, p := range k.Properties {
			*out = append(*out, leafError{
				loc:     append(slices.Clone(ve.InstanceLocation), p),
				message: "unknown key",
				keyNode: true,
			})
		}
	case *kind.Type:
		*out = append(*out, leafError{
			loc:     ve.InstanceLocation,
			message: fmt.Sprintf("wrong type: got %s, want %s", k.Got, strings.Join(k.Want, " or ")),
		})
	default:
		*out = append(*out, leafError{loc: ve.InstanceLocation, message: leafMessage(ve)})
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

func (s *schemaDoc) typeAt(path []string) string {
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

// envLeaves lists keys that env vars can set: scalars and string arrays.
func (s *schemaDoc) envLeaves() []string {
	var out []string
	var walk func(node map[string]any, prefix []string)
	walk = func(node map[string]any, prefix []string) {
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return
		}
		for name, v := range props {
			child, isMap := v.(map[string]any)
			if !isMap {
				continue
			}
			path := append(slices.Clone(prefix), name)
			switch child["type"] {
			case "object":
				walk(child, path)
			case typeArray:
				if items, itemsOK := child["items"].(map[string]any); itemsOK && items["type"] == typeString {
					out = append(out, strings.Join(path, "."))
				}
			default:
				out = append(out, strings.Join(path, "."))
			}
		}
	}
	walk(s.raw, nil)
	slices.Sort(out)
	return out
}

type parsedFile struct {
	path string
	data map[string]any
	node *yaml.Node
}

func parseFile(s *schemaDoc, path string, b []byte) (*parsedFile, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, &ValidationError{Problems: []Problem{yamlProblem(path, err)}}
	}
	var data any
	if err := root.Decode(&data); err != nil {
		return nil, &ValidationError{Problems: []Problem{yamlProblem(path, err)}}
	}
	if data == nil { // empty or comment-only file
		data = map[string]any{}
	}
	inst, err := toJSONValue(data)
	if err != nil {
		return nil, &ValidationError{Problems: []Problem{{Origin: path, Message: err.Error()}}}
	}
	leaves, err := s.validate(inst)
	if err != nil {
		return nil, err
	}
	if len(leaves) > 0 {
		problems := make([]Problem, 0, len(leaves))
		for _, l := range leaves {
			p := Problem{Origin: path, Key: strings.Join(l.loc, "."), Message: l.message}
			if n := nodeAt(&root, l.loc, l.keyNode); n != nil {
				p.Line, p.Column = n.Line, n.Column
			}
			problems = append(problems, p)
		}
		slices.SortStableFunc(problems, func(a, b Problem) int {
			if a.Line != b.Line {
				return a.Line - b.Line
			}
			return a.Column - b.Column
		})
		return nil, &ValidationError{Problems: problems}
	}
	m, ok := data.(map[string]any)
	if !ok { // unreachable: the schema requires an object
		return nil, &ValidationError{Problems: []Problem{{Origin: path, Message: "top level must be a mapping"}}}
	}
	return &parsedFile{path: path, data: m, node: &root}, nil
}

func yamlProblem(path string, err error) Problem {
	// yaml errors already carry "line N:" positions.
	return Problem{Origin: path, Message: "invalid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
}

func toJSONValue(v any) (any, error) {
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

// nodeAt returns the YAML node at loc, or the deepest node that exists.
func nodeAt(root *yaml.Node, loc []string, keyNode bool) *yaml.Node {
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
