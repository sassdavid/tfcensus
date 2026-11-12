package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// Kind names the command a saved envelope came from.
type Kind string

// Kinds of saved envelopes.
const (
	KindScan  Kind = "scan"
	KindDrift Kind = "drift"
	KindAudit Kind = "audit"
	KindIndex Kind = "index" // inventory --save-index
)

// ErrNotEnvelope is returned for a file that isn't a tfcensus envelope of a known kind.
var ErrNotEnvelope = errors.New("not a tfcensus scan, drift, audit or index envelope")

// Saved is one envelope read from a file. Exactly one of the report fields is set.
type Saved struct {
	Kind        Kind
	Path        string
	Version     string
	GeneratedAt time.Time
	Errors      []render.Error
	Scan        *scan.Report
	Drift       *drift.Report
	Audit       *audit.Report
	Index       *tfstate.Index
}

// Read reads an envelope written with --format json or --save-index (plain, .gz or .zst) and
// tells its kind from the data's fields. Drift attribute values are dropped, so nothing built
// from a Saved can show them.
func Read(path string) (*Saved, error) {
	var env struct {
		SchemaVersion string          `json:"schema_version"`
		Tool          string          `json:"tool"`
		Version       string          `json:"version"`
		GeneratedAt   time.Time       `json:"generated_at"`
		Data          json.RawMessage `json:"data"`
		Errors        []render.Error  `json:"errors"`
	}
	if err := tfstate.ReadIndexFile(path, &env); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if env.Tool != render.ToolName || env.SchemaVersion != render.SchemaVersion {
		return nil, fmt.Errorf("%s: %w", path, ErrNotEnvelope)
	}
	s := &Saved{Path: path, Version: env.Version, GeneratedAt: env.GeneratedAt, Errors: env.Errors}
	if s.Errors == nil {
		s.Errors = []render.Error{}
	}
	kind, err := kindOf(env.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Kind = kind
	if err = s.decode(env.Data); err != nil {
		return nil, fmt.Errorf("decode %s %s: %w", kind, path, err)
	}
	return s, nil
}

func kindOf(data json.RawMessage) (Kind, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil || keys == nil {
		return "", ErrNotEnvelope
	}
	has := func(names ...string) bool {
		for _, n := range names {
			if _, ok := keys[n]; !ok {
				return false
			}
		}
		return true
	}
	switch {
	case has("mode", "units", "changes"):
		return KindDrift, nil
	case has("checks", "lock_tables", "findings"):
		return KindAudit, nil
	case has("accounts", "regions", "findings", "runs"):
		return KindScan, nil
	case has("units", "entries"):
		return KindIndex, nil
	}
	return "", ErrNotEnvelope
}

func (s *Saved) decode(data json.RawMessage) error {
	var err error
	switch s.Kind {
	case KindScan:
		s.Scan = &scan.Report{}
		err = json.Unmarshal(data, s.Scan)
	case KindDrift:
		s.Drift = &drift.Report{}
		if err = json.Unmarshal(data, s.Drift); err == nil {
			StripValues(s.Drift)
		}
	case KindAudit:
		s.Audit = &audit.Report{}
		err = json.Unmarshal(data, s.Audit)
	case KindIndex:
		s.Index = &tfstate.Index{}
		err = json.Unmarshal(data, s.Index)
	}
	if err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

// StripValues drops the before and after values that drift --show-attributes keeps.
func StripValues(r *drift.Report) {
	strip := func(attrs []drift.Attr) {
		for i := range attrs {
			attrs[i].Before, attrs[i].After = nil, nil
		}
	}
	for i := range r.Changes {
		strip(r.Changes[i].Attributes)
	}
	for i := range r.Groups {
		strip(r.Groups[i].Attributes)
	}
}

// ReadAll reads every path, in order.
func ReadAll(paths []string) ([]*Saved, error) {
	out := make([]*Saved, 0, len(paths))
	for _, p := range paths {
		s, err := Read(p)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
