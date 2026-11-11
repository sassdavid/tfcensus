package iampolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Effect is a statement's Effect.
type Effect string

// Statement effects.
const (
	Allow Effect = "Allow"
	Deny  Effect = "Deny"
)

// Document is a parsed policy.
type Document struct {
	Statements []Statement
}

// Statement is one policy statement. Every list holds the values as written; single values
// become one-element lists.
type Statement struct {
	Sid          string
	Effect       Effect
	Principal    Principals // nil when the statement has no Principal element
	NotPrincipal Principals
	Action       []string
	NotAction    []string
	Resource     []string
	NotResource  []string
	Condition    []Condition
}

// Principals maps a principal type (AWS, Service, Federated, CanonicalUser, or *) to its values.
type Principals map[string][]string

// Condition is one operator and key with its values, such as Bool aws:SecureTransport [false].
type Condition struct {
	Operator string
	Key      string // lowercase: condition keys are case-insensitive
	Values   []string
}

type rawDocument struct {
	Statement json.RawMessage `json:"Statement"`
}

type rawStatement struct {
	Sid          string                                `json:"Sid"`
	Effect       Effect                                `json:"Effect"`
	Principal    json.RawMessage                       `json:"Principal"`
	NotPrincipal json.RawMessage                       `json:"NotPrincipal"`
	Action       json.RawMessage                       `json:"Action"`
	NotAction    json.RawMessage                       `json:"NotAction"`
	Resource     json.RawMessage                       `json:"Resource"`
	NotResource  json.RawMessage                       `json:"NotResource"`
	Condition    map[string]map[string]json.RawMessage `json:"Condition"`
}

// Parse reads a policy document. Statement may be a single object or a list.
func Parse(b []byte) (*Document, error) {
	var raw rawDocument
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	var stmts []rawStatement
	switch trimmed := bytes.TrimSpace(raw.Statement); {
	case len(trimmed) == 0:
		return nil, errors.New("parse policy: no Statement")
	case trimmed[0] == '{':
		var s rawStatement
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("parse policy statement: %w", err)
		}
		stmts = []rawStatement{s}
	default:
		if err := json.Unmarshal(trimmed, &stmts); err != nil {
			return nil, fmt.Errorf("parse policy statements: %w", err)
		}
	}
	doc := &Document{Statements: make([]Statement, 0, len(stmts))}
	for i := range stmts {
		s, err := stmts[i].statement()
		if err != nil {
			return nil, fmt.Errorf("parse policy statement %d: %w", i, err)
		}
		doc.Statements = append(doc.Statements, s)
	}
	return doc, nil
}

func (r *rawStatement) statement() (Statement, error) {
	s := Statement{Sid: r.Sid, Effect: r.Effect}
	if s.Effect != Allow && s.Effect != Deny {
		return s, fmt.Errorf("invalid Effect %q", r.Effect)
	}
	var err error
	if len(bytes.TrimSpace(r.Principal)) > 0 {
		if s.Principal, err = principals(r.Principal); err != nil {
			return s, fmt.Errorf("principal: %w", err)
		}
	}
	if len(bytes.TrimSpace(r.NotPrincipal)) > 0 {
		if s.NotPrincipal, err = principals(r.NotPrincipal); err != nil {
			return s, fmt.Errorf("not principal: %w", err)
		}
	}
	lists := []struct {
		raw json.RawMessage
		dst *[]string
	}{
		{r.Action, &s.Action},
		{r.NotAction, &s.NotAction},
		{r.Resource, &s.Resource},
		{r.NotResource, &s.NotResource},
	}
	for _, l := range lists {
		if *l.dst, err = values(l.raw); err != nil {
			return s, err
		}
	}
	for op, keys := range r.Condition {
		for key, raw := range keys {
			vals, valErr := values(raw)
			if valErr != nil {
				return s, fmt.Errorf("condition %s %s: %w", op, key, valErr)
			}
			s.Condition = append(s.Condition, Condition{Operator: op, Key: strings.ToLower(key), Values: vals})
		}
	}
	return s, nil
}

// principals reads "*" or a map of type to one value or a list.
func principals(raw json.RawMessage) (Principals, error) {
	var star string
	if json.Unmarshal(raw, &star) == nil {
		if star != "*" {
			return nil, fmt.Errorf("want \"*\" or an object, got %q", star)
		}
		return Principals{"*": {"*"}}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("want \"*\" or an object: %w", err)
	}
	out := Principals{}
	for typ, v := range m {
		vals, err := values(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", typ, err)
		}
		out[typ] = vals
	}
	return out, nil
}

// values reads one scalar or a list of scalars as strings. Booleans and numbers keep their JSON
// spelling, so "aws:SecureTransport": false reads as "false".
func values(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] != '[' {
		v, err := scalar(trimmed)
		if err != nil {
			return nil, err
		}
		return []string{v}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("read list: %w", err)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		v, err := scalar(bytes.TrimSpace(it))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func scalar(raw []byte) (string, error) {
	switch {
	case len(raw) == 0:
		return "", errors.New("empty value")
	case raw[0] == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("read string: %w", err)
		}
		return s, nil
	case string(raw) == "true" || string(raw) == "false":
		return string(raw), nil
	default:
		if _, err := strconv.ParseFloat(string(raw), 64); err != nil {
			return "", fmt.Errorf("want a string, boolean or number, got %s", raw)
		}
		return string(raw), nil
	}
}
