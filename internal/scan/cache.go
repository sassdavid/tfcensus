package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// snapshotFormat changes when [Snapshot] changes shape, so old cache files are ignored.
const snapshotFormat = 1

// Snapshot is what a scan collected before matching: the live inventory, the state index and
// the problems found on the way. Rules aren't part of it, so a re-run with other rules can reuse
// it.
type Snapshot struct {
	Format    int               `json:"format"`
	Key       string            `json:"key"`
	Created   time.Time         `json:"created"`
	Inventory *inventory.Result `json:"inventory"`
	Index     *tfstate.Index    `json:"index"`
	Problems  []Problem         `json:"problems"`
}

// Problem is an error a scan hit before matching, such as a state it couldn't read. Auth is true
// when credentials caused it.
type Problem struct {
	Target  string `json:"target"`
	Message string `json:"message"`
	Auth    bool   `json:"auth,omitempty"`
}

// Cache keeps snapshots as zstd-compressed JSON files under Dir.
type Cache struct {
	Dir string // empty turns the cache off
	TTL time.Duration
	Now func() time.Time
}

// Key returns the cache key of a scan's inputs: a hash of their JSON encoding.
func Key(inputs any) (string, error) {
	b, err := json.Marshal(inputs)
	if err != nil {
		return "", fmt.Errorf("encode cache key: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16]), nil
}

func (c *Cache) path(key string) string { return filepath.Join(c.Dir, "scan", key+".json.zst") }

// Load returns the snapshot stored under key when it is younger than the TTL. A missing, stale
// or unreadable file is a miss, not an error.
func (c *Cache) Load(key string) (*Snapshot, bool) {
	if c == nil || c.Dir == "" {
		return nil, false
	}
	var s Snapshot
	if err := tfstate.ReadIndexFile(c.path(key), &s); err != nil {
		return nil, false
	}
	if s.Format != snapshotFormat || s.Key != key || c.Now().Sub(s.Created) >= c.TTL || s.Inventory == nil ||
		s.Index == nil {
		return nil, false
	}
	return &s, true
}

// Save stores s under s.Key.
func (c *Cache) Save(s *Snapshot) error {
	if c == nil || c.Dir == "" {
		return nil
	}
	s.Format = snapshotFormat
	dir := filepath.Dir(c.path(s.Key))
	if err := os.MkdirAll(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create scan cache: %w", err)
	}
	if err := tfstate.WriteIndexFile(c.path(s.Key), s); err != nil {
		return fmt.Errorf("write scan cache: %w", err)
	}
	return nil
}
