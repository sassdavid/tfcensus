package sarif

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Version and SchemaURI identify SARIF 2.1.0.
const (
	Version   = "2.1.0"
	SchemaURI = "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json"
)

// Levels a result can have.
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelNote    = "note"
)

// Log is a SARIF log with one run.
type Log struct {
	Schema  string `json:"$schema"`
	Version string `json:"version"`
	Runs    []Run  `json:"runs"`
}

// Run is one tool invocation.
type Run struct {
	Tool    Tool     `json:"tool"`
	Results []Result `json:"results"`
}

// Tool describes the tool and its rules.
type Tool struct {
	Driver Driver `json:"driver"`
}

// Driver is the tool's main component.
type Driver struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
	InformationURI string `json:"informationUri,omitempty"`
	Rules          []Rule `json:"rules"`
}

// Rule is one reportingDescriptor.
type Rule struct {
	ID                   string         `json:"id"`
	ShortDescription     Message        `json:"shortDescription"`
	FullDescription      Message        `json:"fullDescription,omitzero"`
	Help                 Message        `json:"help"`
	DefaultConfiguration Configuration  `json:"defaultConfiguration"`
	Properties           map[string]any `json:"properties,omitempty"`
}

// Configuration holds a rule's default level.
type Configuration struct {
	Level string `json:"level"`
}

// Message is a plain-text message.
type Message struct {
	Text string `json:"text"`
}

// Result is one finding.
type Result struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             Message           `json:"message"`
	Locations           []Location        `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
	Suppressions        []Suppression     `json:"suppressions,omitempty"`
	Properties          map[string]any    `json:"properties,omitempty"`
}

// Location points at an artifact by URI.
type Location struct {
	PhysicalLocation PhysicalLocation `json:"physicalLocation"`
}

// PhysicalLocation names an artifact and, when known, a line in it.
type PhysicalLocation struct {
	ArtifactLocation ArtifactLocation `json:"artifactLocation"`
	Region           *Region          `json:"region,omitempty"`
}

// Region is a line of an artifact.
type Region struct {
	StartLine int `json:"startLine"` // 1-based
}

// ArtifactLocation is a URI, relative to the repository root or absolute (s3://, dynamodb://).
type ArtifactLocation struct {
	URI string `json:"uri"`
}

const fileScheme = "file"

// FileURI returns the artifact URI of a slash-separated path. A relative path stays relative to
// the repository root, as GitHub code scanning prefers; an absolute one (/src, C:/src, //host/share)
// becomes a file URI, so Windows paths never reach the log as bare drive letters.
func FileURI(path string) string {
	switch {
	case strings.HasPrefix(path, "//"):
		host, rest, _ := strings.Cut(path[2:], "/")
		return (&url.URL{Scheme: fileScheme, Host: host, Path: "/" + rest}).String()
	case strings.HasPrefix(path, "/"):
		return (&url.URL{Scheme: fileScheme, Path: path}).String()
	case len(path) >= 3 && path[1] == ':' && path[2] == '/' && isLetter(path[0]):
		return (&url.URL{Scheme: fileScheme, Path: "/" + path}).String()
	default:
		return (&url.URL{Path: path}).String()
	}
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// Suppression records why a result is accepted.
type Suppression struct {
	Kind          string `json:"kind"` // external: from configuration, not the source
	Status        string `json:"status"`
	Justification string `json:"justification"`
}

// New returns an empty log for a tool.
func New(name, version, infoURI string) *Log {
	return &Log{
		Schema:  SchemaURI,
		Version: Version,
		Runs: []Run{{
			Tool:    Tool{Driver: Driver{Name: name, Version: version, InformationURI: infoURI, Rules: []Rule{}}},
			Results: []Result{},
		}},
	}
}

// Write encodes l as indented JSON.
func Write(w io.Writer, l *Log) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(l); err != nil {
		return fmt.Errorf("encode sarif: %w", err)
	}
	return nil
}
