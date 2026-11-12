package migrate

import (
	"slices"
	"time"
)

// HistorySuffix follows a unit's state key to name its history object.
const HistorySuffix = ".tfcensus-migrations.json"

const historyVersion = 1

// Roles a unit plays in a block.
const (
	RoleSource      = "source"
	RoleDestination = "destination"
	RoleInPlace     = "in-place"
)

// History is the migration history of one unit, stored next to its state.
type History struct {
	Version int      `json:"version"`
	Unit    string   `json:"unit"`
	Records []Record `json:"records"`
}

// Record is one applied block in one unit.
type Record struct {
	File         string      `json:"file"`
	Block        string      `json:"block"`
	Role         string      `json:"role"`
	Strategy     string      `json:"strategy"`
	AppliedAt    time.Time   `json:"applied_at"`
	By           string      `json:"by"`
	VersionIDs   *VersionIDs `json:"version_ids,omitempty"` // nil for mark-applied
	Manual       bool        `json:"manual,omitempty"`      // recorded by mark-applied
	RolledBackAt *time.Time  `json:"rolled_back_at,omitempty"`
}

// VersionIDs are the S3 object versions of the state around a push. An empty Before means the
// state didn't exist.
type VersionIDs struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// active returns the record of file and block that hasn't been rolled back, or nil.
func (h *History) active(file, block string) *Record {
	if h == nil {
		return nil
	}
	for i := range slices.Backward(h.Records) {
		r := &h.Records[i]
		if r.File == file && r.Block == block && r.RolledBackAt == nil {
			return r
		}
	}
	return nil
}
