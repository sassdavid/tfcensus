package mcpkit

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sassdavid/tfcensus/internal/render"
)

// ErrCursor is returned for a cursor the server doesn't know, has dropped, or issued for
// another tool.
var ErrCursor = errors.New("unknown or expired cursor")

// Pager splits an envelope's data into items, so a result over the cap can be sent in pages.
type Pager interface {
	Len() int
	Noun() string        // names the items in the page field, such as "entries"
	Order() []int        // item indexes in the order pages take them; nil means 0 to Len-1
	Data(keep []int) any // the data with only the items at keep, which is sorted
}

// SlicePager pages items. Items with a lower rank go to earlier pages; equal ranks keep their
// order. data builds the envelope data from a subset of items in their original order.
func SlicePager[T any](items []T, noun string, rank func(T) int, data func([]T) any) Pager {
	return slicePager[T]{items: items, noun: noun, rank: rank, data: data}
}

type slicePager[T any] struct {
	items []T
	noun  string
	rank  func(T) int
	data  func([]T) any
}

func (p slicePager[T]) Len() int     { return len(p.items) }
func (p slicePager[T]) Noun() string { return p.noun }

func (p slicePager[T]) Order() []int {
	order := make([]int, len(p.items))
	for i := range order {
		order[i] = i
	}
	if p.rank != nil {
		slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(p.rank(p.items[a]), p.rank(p.items[b])) })
	}
	return order
}

func (p slicePager[T]) Data(keep []int) any {
	out := make([]T, len(keep))
	for i, k := range keep {
		out[i] = p.items[k]
	}
	return p.data(out)
}

const (
	cursorIDBytes   = 16
	cursorPageBytes = 4
)

// encode writes v as compact JSON, with the escaping [render.JSON] uses, so [json.Indent] with
// two spaces gives the --format json bytes.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode json: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// snapshot is a split result: pages are cut lazily and remembered, so a repeated cursor
// returns the same page.
type snapshot struct {
	id     string
	tool   string
	env    render.Envelope
	pager  Pager
	order  []int
	starts []int // starts[n] is the index into order where page n begins
	max    int
	used   time.Time
}

// store keeps split results in memory until they expire or make room for newer ones.
type store struct {
	mu    sync.Mutex
	clock func() time.Time
	ttl   time.Duration
	limit int
	byID  map[string]*snapshot
}

func newStore(clock func() time.Time, ttl time.Duration, limit int) *store {
	return &store{clock: clock, ttl: ttl, limit: limit, byID: map[string]*snapshot{}}
}

// first returns the whole result when it fits in max bytes, and its first page otherwise.
func (s *store) first(tool string, res *Result, maxBytes int) ([]byte, error) {
	whole, err := encode(res.Envelope)
	if err != nil {
		return nil, err
	}
	if maxBytes < 0 || len(whole) <= maxBytes || res.Pager == nil || res.Pager.Len() == 0 {
		return whole, nil
	}
	id := make([]byte, cursorIDBytes)
	_, _ = rand.Read(id) // never fails; see crypto/rand.Read
	snap := &snapshot{
		id:     string(id),
		tool:   tool,
		env:    res.Envelope,
		pager:  res.Pager,
		order:  res.Pager.Order(),
		starts: []int{0},
		max:    maxBytes,
	}
	if snap.order == nil {
		snap.order = make([]int, res.Pager.Len())
		for i := range snap.order {
			snap.order[i] = i
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	page, err := snap.page(0)
	if err != nil {
		return nil, err
	}
	if len(snap.starts) > 1 && snap.starts[1] < len(snap.order) {
		s.add(snap)
	}
	return page, nil
}

// next returns the page a cursor names.
func (s *store) next(tool, cursor string) ([]byte, error) {
	id, n, err := decodeCursor(cursor)
	if err != nil {
		return nil, fmt.Errorf("%w; call %s again without a cursor", err, tool)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	snap, ok := s.byID[id]
	if !ok || snap.tool != tool {
		return nil, fmt.Errorf("%w; call %s again without a cursor", ErrCursor, tool)
	}
	snap.used = s.clock()
	return snap.page(n)
}

// add keeps snap, dropping the least recently used snapshots beyond the limit.
func (s *store) add(snap *snapshot) {
	s.expire()
	snap.used = s.clock()
	s.byID[snap.id] = snap
	for len(s.byID) > s.limit {
		var oldest *snapshot
		for _, o := range s.byID {
			if oldest == nil || o.used.Before(oldest.used) {
				oldest = o
			}
		}
		delete(s.byID, oldest.id)
	}
}

func (s *store) expire() {
	now := s.clock()
	for id, snap := range s.byID {
		if now.Sub(snap.used) > s.ttl {
			delete(s.byID, id)
		}
	}
}

// page cuts pages up to n and returns page n.
func (p *snapshot) page(n int) ([]byte, error) {
	for len(p.starts) <= n+1 {
		start := p.starts[len(p.starts)-1]
		if start >= len(p.order) {
			return nil, fmt.Errorf("%w; call %s again without a cursor", ErrCursor, p.tool)
		}
		end, err := p.cut(start)
		if err != nil {
			return nil, err
		}
		p.starts = append(p.starts, end)
	}
	return p.render(n, p.starts[n], p.starts[n+1])
}

// cut returns the end of the page that starts at start: as many items as fit in max bytes, and
// at least one. Sizes grow with every item, so a binary search finds the end.
func (p *snapshot) cut(start int) (int, error) {
	var failed error
	k := sort.Search(len(p.order)-start, func(k int) bool {
		if failed != nil {
			return true
		}
		// The real cursor has the placeholder's length, so the measured size is exact.
		b, err := p.encode(start, start+k+1, placeholderCursor())
		if err != nil {
			failed = err
			return true
		}
		return len(b) > p.max
	})
	if failed != nil {
		return 0, failed
	}
	return start + max(k, 1), nil
}

func (p *snapshot) render(n, start, end int) ([]byte, error) {
	cursor := ""
	if end < len(p.order) {
		cursor = encodeCursor(p.id, n+1)
	}
	return p.encode(start, end, cursor)
}

// encode returns the page with the items order[start:end]; cursor is empty on the last page.
func (p *snapshot) encode(start, end int, cursor string) ([]byte, error) {
	keep := slices.Clone(p.order[start:end])
	slices.Sort(keep)
	env := p.env
	env.Data = p.pager.Data(keep)
	env.Page = &render.Page{Items: p.pager.Noun(), Returned: len(keep), Total: len(p.order), NextCursor: cursor}
	return encode(env)
}

func encodeCursor(id string, n int) string {
	b := make([]byte, 0, cursorIDBytes+cursorPageBytes)
	b = append(b, id...)
	b = binary.BigEndian.AppendUint32(b, uint32(n)) //nolint:gosec // n counts pages and stays small
	return base64.RawURLEncoding.EncodeToString(b)
}

func placeholderCursor() string {
	return encodeCursor(string(make([]byte, cursorIDBytes)), 0)
}

func decodeCursor(c string) (string, int, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || len(b) != cursorIDBytes+cursorPageBytes {
		return "", 0, ErrCursor
	}
	return string(b[:cursorIDBytes]), int(binary.BigEndian.Uint32(b[cursorIDBytes:])), nil
}
