package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sassdavid/tfcensus/internal/yamlschema"
	"github.com/sassdavid/tfcensus/schemas"
)

// ErrInvalid is wrapped by every configuration error.
var ErrInvalid = errors.New("invalid configuration")

// Problem is one configuration error and where it is.
type Problem = yamlschema.Problem

// ValidationError lists every [Problem] found. It unwraps to [ErrInvalid].
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string { return strings.Join(e.ProblemLines(), "\n") }

func (*ValidationError) Unwrap() error { return ErrInvalid }

// Header introduces the problem list in error output.
func (*ValidationError) Header() string { return "invalid configuration" }

// ProblemLines returns one line per problem, with its origin and position.
func (e *ValidationError) ProblemLines() []string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return lines
}

const (
	typeArray  = "array"
	typeString = "string"
)

const configSchemaURL = "https://github.com/sassdavid/tfcensus/schemas/" + schemas.ConfigV1

func loadSchema() (*yamlschema.Schema, error) {
	b, err := schemas.Read(schemas.ConfigV1)
	if err != nil {
		return nil, err
	}
	s, err := yamlschema.Compile(configSchemaURL, b)
	if err != nil {
		return nil, fmt.Errorf("load config schema: %w", err)
	}
	return s, nil
}

// envLeaves lists keys that env vars can set: scalars and string arrays.
func envLeaves(s *yamlschema.Schema) []string {
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
	walk(s.Raw(), nil)
	slices.Sort(out)
	return out
}

type parsedFile struct {
	path string
	data map[string]any
}

func parseFile(s *yamlschema.Schema, path string, b []byte) (*parsedFile, error) {
	doc, problems, err := s.Parse(path, b)
	if err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	m, ok := doc.Data.(map[string]any)
	if !ok { // unreachable: the schema requires an object
		return nil, &ValidationError{Problems: []Problem{{Origin: path, Message: "top level must be a mapping"}}}
	}
	return &parsedFile{path: path, data: m}, nil
}
