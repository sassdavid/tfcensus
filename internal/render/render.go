package render

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Format is an output format selected with --format.
type Format string

// Output formats. FormatAuto resolves to tty or plain depending on stdout.
const (
	FormatAuto    Format = ""
	FormatTTY     Format = "tty"
	FormatPlain   Format = "plain"
	FormatMD      Format = "md"
	FormatJSON    Format = "json"
	FormatCSV     Format = "csv"
	FormatSARIF   Format = "sarif"
	FormatGitHub  Format = "github"  // GitHub Actions workflow commands, which show as annotations
	FormatOpenVEX Format = "openvex" // an OpenVEX v0.2.0 document
)

// AllFormats lists the formats --format accepts.
func AllFormats() []Format {
	return []Format{FormatTTY, FormatPlain, FormatMD, FormatJSON, FormatCSV, FormatSARIF, FormatGitHub, FormatOpenVEX}
}

// ParseFormat validates a --format value. The empty string means auto.
func ParseFormat(s string) (Format, error) {
	f := Format(s)
	if f == FormatAuto {
		return f, nil
	}
	if slices.Contains(AllFormats(), f) {
		return f, nil
	}
	return "", fmt.Errorf("invalid format %q: want one of %s", s, joinFormats(AllFormats()))
}

// Resolve turns FormatAuto into tty, when stdout is a terminal or gets color anyway (--color
// always, CLICOLOR_FORCE), or plain.
func (f Format) Resolve(stdoutIsTTY bool) Format {
	if f != FormatAuto {
		return f
	}
	if stdoutIsTTY {
		return FormatTTY
	}
	return FormatPlain
}

// Check returns an error unless f is one of allowed.
func (f Format) Check(allowed ...Format) error {
	if slices.Contains(allowed, f) {
		return nil
	}
	return fmt.Errorf("format %q is not supported by this command: want one of %s", f, joinFormats(allowed))
}

func joinFormats(fs []Format) string {
	s := make([]string, len(fs))
	for i, f := range fs {
		s[i] = string(f)
	}
	return strings.Join(s, ", ")
}

// SchemaVersion is the envelope version in schemas/report-v1.json.
const SchemaVersion = "1"

// ToolName is the value of the envelope's "tool" field.
const ToolName = "tfcensus"

// Envelope wraps every JSON output.
type Envelope struct {
	SchemaVersion string    `json:"schema_version"`
	Tool          string    `json:"tool"`
	Version       string    `json:"version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Data          any       `json:"data"`
	Errors        []Error   `json:"errors"`
	Page          *Page     `json:"page,omitempty"` // set by the MCP server when it splits a result
}

// Page says which part of a split result an envelope holds.
type Page struct {
	Items      string `json:"items"`                 // what was split, such as "entries"
	Returned   int    `json:"returned"`              // items in this page
	Total      int    `json:"total"`                 // items in the whole result
	NextCursor string `json:"next_cursor,omitempty"` // empty on the last page
}

// Error is a per-target error carried in the envelope.
type Error struct {
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

// NewEnvelope returns an envelope with no errors, stamped with now in UTC to the second.
func NewEnvelope(version string, now time.Time, data any) Envelope {
	return Envelope{
		SchemaVersion: SchemaVersion,
		Tool:          ToolName,
		Version:       version,
		GeneratedAt:   now.UTC().Truncate(time.Second),
		Data:          data,
		Errors:        []Error{},
	}
}

// JSON writes v as indented JSON followed by a newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}
