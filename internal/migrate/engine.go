package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/prompt"
)

// Errors that stop a run.
var (
	ErrRejected   = errors.New("verification rejected the migration")
	ErrPartial    = errors.New("block is applied in some units only")
	ErrManual     = errors.New("block was marked applied by hand")
	ErrUnknown    = errors.New("unit not found")
	ErrNoState    = errors.New("source state does not exist")
	ErrNotApplied = errors.New("block is not applied")
)

// Block statuses.
const (
	StatusPending = "pending"
	StatusApplied = "applied"
	StatusPartial = "partial"
)

// Block results.
const (
	ResultVerified   = "verified"
	ResultRejected   = "rejected"
	ResultSkipped    = "skipped"
	ResultApplied    = "applied"
	ResultPartial    = "partially-applied"
	ResultWritten    = "written"
	ResultRolledBack = "rolled-back"
	ResultMarked     = "marked-applied"
	ResultPlanned    = "planned"
)

// Finding rules.
const (
	RuleVerify     = "MV-VERIFY"
	RulePlan       = "MV-PLAN"
	RuleAccount    = "MV-ACCOUNT"
	RuleVersioning = "MV-VERSIONING"
)

// Planner plans a unit, against a local state copy when stateFile is set.
type Planner interface {
	Plan(ctx context.Context, dir, stateFile string) ([]ResourceChange, error)
}

// Pusher pushes a state file to a unit's backend.
type Pusher interface {
	Push(ctx context.Context, dir, stateFile string) error
}

// ClientsFunc returns the backend clients of a unit.
type ClientsFunc func(ctx context.Context, u *Unit) (*Clients, error)

// Options configures an [Engine].
type Options struct {
	Units   map[string]*Unit // by path
	Clients ClientsFunc
	Planner Planner
	Pusher  Pusher // nil for read-only commands
	Logger  *slog.Logger
	Clock   func() time.Time
}

// Report is the outcome of a move command.
type Report struct {
	File    string        `json:"file"`
	Command string        `json:"command"`
	Blocks  []BlockReport `json:"blocks"`
}

// BlockReport is the outcome for one block.
type BlockReport struct {
	Name     string       `json:"name"`
	Strategy string       `json:"strategy"`
	From     string       `json:"from"`
	Status   string       `json:"status"`
	Result   string       `json:"result,omitempty"`
	Units    []UnitReport `json:"units"`
	Findings []Finding    `json:"findings"`
	Notes    []string     `json:"notes,omitempty"`
}

// UnitReport is what a block does to one unit. Addresses are identifiers; no attribute value is
// ever reported.
type UnitReport struct {
	Path       string        `json:"path"`
	Role       string        `json:"role"`
	Status     string        `json:"status,omitempty"`
	Moved      []string      `json:"moved,omitempty"`
	Skipped    []string      `json:"skipped,omitempty"`
	Serial     *SerialChange `json:"serial,omitempty"`
	NewState   bool          `json:"new_state,omitempty"`
	File       string        `json:"file,omitempty"`
	VersionIDs *VersionIDs   `json:"version_ids,omitempty"`
}

// SerialChange is a state serial before and after a block.
type SerialChange struct {
	Before uint64 `json:"before"`
	After  uint64 `json:"after"`
}

// Finding is a reason a block can't be applied.
type Finding struct {
	Rule    string `json:"rule"`
	Unit    string `json:"unit"`
	Address string `json:"address,omitempty"`
	Reason  string `json:"reason"`
}

// Engine runs migration blocks. It keeps the states it read, so blocks that touch the same unit
// see each other's changes.
type Engine struct {
	o       Options
	tmp     string
	clients map[string]*Clients
	cur     map[string]*current
	hist    map[string]*histEntry
}

type current struct {
	snap  snapshot
	state *State // nil when the unit has no state yet
}

type histEntry struct {
	h    *History
	etag string
}

// pending is what applying a planned block writes.
type pending struct {
	order  []string          // units in push order: destinations, then the source
	states map[string]*State // surgery
	files  map[string][]byte // native
	snaps  map[string]snapshot
	roles  map[string]string
}

// New returns an Engine; call Close when done.
func New(o Options) (*Engine, error) {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	tmp, err := os.MkdirTemp("", "tfcensus-move-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	return &Engine{
		o: o, tmp: tmp, clients: map[string]*Clients{}, cur: map[string]*current{}, hist: map[string]*histEntry{},
	}, nil
}

// Close removes the engine's temp files, which hold state copies.
func (e *Engine) Close() error { return os.RemoveAll(e.tmp) }

// Select returns the blocks named in names, or every block when names is empty.
func Select(f *File, names []string) ([]*Block, error) {
	if len(names) == 0 {
		return f.Blocks, nil
	}
	var out []*Block
	for _, n := range names {
		i := slices.IndexFunc(f.Blocks, func(b *Block) bool { return b.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("%w: no block %q in %s", ErrFile, n, f.Name)
		}
		out = append(out, f.Blocks[i])
	}
	return out, nil
}

func (e *Engine) unit(path string) (*Unit, error) {
	u, ok := e.o.Units[path]
	if !ok {
		return nil, fmt.Errorf("%w: %s (it needs a resolved s3 backend)", ErrUnknown, path)
	}
	return u, nil
}

func (e *Engine) client(ctx context.Context, u *Unit) (*Clients, error) {
	if c, ok := e.clients[u.Path]; ok {
		return c, nil
	}
	c, err := e.o.Clients(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("backend credentials of %s: %w", u.Path, err)
	}
	e.clients[u.Path] = c
	return c, nil
}

func (e *Engine) load(ctx context.Context, path string) (*current, error) {
	if c, ok := e.cur[path]; ok {
		return c, nil
	}
	u, err := e.unit(path)
	if err != nil {
		return nil, err
	}
	cl, err := e.client(ctx, u)
	if err != nil {
		return nil, err
	}
	snap, err := pull(ctx, cl.Reader, u)
	if err != nil {
		return nil, err
	}
	c := &current{snap: snap}
	if snap.exists {
		if c.state, err = ParseState(snap.body); err != nil {
			return nil, fmt.Errorf("%s: %w", u.location(), err)
		}
	}
	e.cur[path] = c
	return c, nil
}

func (e *Engine) history(ctx context.Context, path string) (*histEntry, error) {
	if h, ok := e.hist[path]; ok {
		return h, nil
	}
	u, err := e.unit(path)
	if err != nil {
		return nil, err
	}
	cl, err := e.client(ctx, u)
	if err != nil {
		return nil, err
	}
	h, etag, err := readHistory(ctx, cl.Reader, u)
	if err != nil {
		return nil, err
	}
	e.hist[path] = &histEntry{h: h, etag: etag}
	return e.hist[path], nil
}

func roleOf(b *Block, path string) string {
	switch {
	case len(b.To) == 0:
		return RoleInPlace
	case path == b.From:
		return RoleSource
	default:
		return RoleDestination
	}
}

// status returns the block's status from the units' histories, and each unit's active record.
func (e *Engine) status(ctx context.Context, f *File, b *Block) (string, map[string]*Record, error) {
	recs := map[string]*Record{}
	for _, p := range b.Units() {
		h, err := e.history(ctx, p)
		if err != nil {
			return "", nil, err
		}
		if r := h.h.active(f.Name, b.Name); r != nil {
			recs[p] = r
		}
	}
	switch len(recs) {
	case 0:
		return StatusPending, recs, nil
	case len(b.Units()):
		return StatusApplied, recs, nil
	default:
		return StatusPartial, recs, nil
	}
}

func newBlockReport(b *Block, status string) *BlockReport {
	rep := &BlockReport{Name: b.Name, Strategy: b.Strategy, From: b.From, Status: status, Findings: []Finding{}}
	for _, p := range b.Units() {
		rep.Units = append(rep.Units, UnitReport{Path: p, Role: roleOf(b, p)})
	}
	return rep
}

func (r *BlockReport) unit(path string) *UnitReport {
	for i := range r.Units {
		if r.Units[i].Path == path {
			return &r.Units[i]
		}
	}
	return nil
}

// planBlock computes the block's new states or files and verifies them, without writing anything
// but temp files.
func (e *Engine) planBlock(ctx context.Context, f *File, b *Block) (*BlockReport, *pending, error) {
	status, _, err := e.status(ctx, f, b)
	if err != nil {
		return nil, nil, err
	}
	rep := newBlockReport(b, status)
	if status != StatusPending {
		rep.Result = ResultSkipped
		return rep, nil, nil
	}
	p := &pending{
		order: b.Units(), states: map[string]*State{}, files: map[string][]byte{},
		snaps: map[string]snapshot{}, roles: map[string]string{},
	}
	for _, path := range p.order {
		c, loadErr := e.load(ctx, path)
		if loadErr != nil {
			return nil, nil, loadErr
		}
		p.snaps[path], p.roles[path] = c.snap, roleOf(b, path)
	}
	if e.cur[b.From].state == nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoState, b.From)
	}
	if len(b.To) == 0 {
		err = e.planActions(b, rep, p)
	} else {
		err = e.planMove(f, b, rep, p)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("block %s: %w", b.Name, err)
	}
	if b.Strategy == StrategySurgery {
		if err = e.checkVersioning(ctx, b, rep); err != nil {
			return nil, nil, err
		}
		if err = e.verify(ctx, b, rep, p); err != nil {
			return nil, nil, err
		}
	}
	rep.Result = ResultVerified
	if b.Strategy == StrategyNative {
		rep.Result = ResultPlanned
	}
	if len(rep.Findings) > 0 {
		rep.Result = ResultRejected
	}
	return rep, p, nil
}

func (e *Engine) planActions(b *Block, rep *BlockReport, p *pending) error {
	cur := e.cur[b.From].state
	st := cur.clone()
	ur := rep.unit(b.From)
	for _, a := range b.actions {
		done, err := a.apply(st)
		if err != nil {
			return err
		}
		ur.Moved = append(ur.Moved, done...)
	}
	st.Serial = cur.Serial + 1
	ur.Serial = &SerialChange{Before: cur.Serial, After: st.Serial}
	p.states[b.From] = st
	return nil
}

func (e *Engine) planMove(f *File, b *Block, rep *BlockReport, p *pending) error {
	srcCur := e.cur[b.From].state
	src := srcCur.clone()
	var all []*Resource
	header := fmt.Sprintf("# Written by tfcensus move from %s, block %s. Delete it after the apply.", f.Name, b.Name)
	srcRep := rep.unit(b.From)
	for _, d := range b.To {
		tk, err := src.take(d.sels)
		if err != nil {
			return fmt.Errorf("to[%q]: %w", d.Unit, err)
		}
		ur := rep.unit(d.Unit)
		ur.Moved, ur.Skipped = tk.moved, tk.skipped
		srcRep.Moved = append(srcRep.Moved, tk.moved...)
		srcRep.Skipped = append(srcRep.Skipped, tk.skipped...)
		all = append(all, tk.resources...)
		e.checkAccounts(d.Unit, tk.resources, rep)
		if b.Strategy == StrategyNative {
			content, notes, genErr := importFile(header, tk.resources)
			if genErr != nil {
				return genErr
			}
			p.files[d.Unit], ur.File = content, NativeFileName(b.Name)
			rep.Notes = append(rep.Notes, notes...)
			continue
		}
		if err = e.moveInto(d.Unit, srcCur.TerraformVersion, tk.resources, rep, p); err != nil {
			return err
		}
	}
	if b.Strategy == StrategyNative {
		content, err := removedFile(header, all, src)
		if err != nil {
			return err
		}
		p.files[b.From], srcRep.File = content, NativeFileName(b.Name)
		return nil
	}
	src.Serial = srcCur.Serial + 1
	srcRep.Serial = &SerialChange{Before: srcCur.Serial, After: src.Serial}
	p.states[b.From] = src
	return nil
}

// moveInto adds moved resources to a copy of the destination's state, or to a new state when the
// unit has none, and bumps its serial.
func (e *Engine) moveInto(dest, tfVersion string, rs []*Resource, rep *BlockReport, p *pending) error {
	ur := rep.unit(dest)
	var dst *State
	if cur := e.cur[dest].state; cur != nil {
		dst = cur.clone()
	} else {
		var err error
		if dst, err = newState(tfVersion); err != nil {
			return err
		}
		ur.NewState = true
	}
	for _, n := range rewriteProviders(rs, dst) {
		rep.Notes = appendOnce(rep.Notes, dest+": provider "+n)
	}
	if err := dst.put(rs); err != nil {
		return fmt.Errorf("to[%q]: %w", dest, err)
	}
	before := dst.Serial
	dst.Serial++
	ur.Serial = &SerialChange{Before: before, After: dst.Serial}
	p.states[dest] = dst
	return nil
}

// checkAccounts adds a finding for each moved resource whose ARN names another account than the
// one the destination deploys to.
func (e *Engine) checkAccounts(dest string, rs []*Resource, rep *BlockReport) {
	u := e.o.Units[dest]
	if u == nil || u.Account == "" {
		return
	}
	for _, r := range rs {
		for i := range r.Instances {
			acct := arnAccount(stringAttr(r.Instances[i], "arn"))
			if acct == "" || acct == u.Account {
				continue
			}
			a, _ := r.instanceAddr(i)
			rep.Findings = append(rep.Findings, Finding{
				Rule:    RuleAccount,
				Unit:    dest,
				Address: a.String(),
				Reason: fmt.Sprintf(
					"the resource is in account %s but %s deploys to account %s",
					acct,
					dest,
					u.Account,
				),
			})
		}
	}
}

func arnAccount(arn string) string {
	parts := strings.SplitN(arn, ":", 6) //nolint:mnd // arn:partition:service:region:account:resource
	if len(parts) < 6 || parts[0] != "arn" {
		return ""
	}
	return parts[4]
}

func (e *Engine) checkVersioning(ctx context.Context, b *Block, rep *BlockReport) error {
	seen := map[string]bool{}
	for _, path := range b.Units() {
		u := e.o.Units[path]
		if seen[u.Bucket] {
			continue
		}
		seen[u.Bucket] = true
		c, err := e.client(ctx, u)
		if err != nil {
			return err
		}
		ok, err := versioned(ctx, c.Reader, u)
		if err != nil {
			return err
		}
		if !ok {
			rep.Findings = append(rep.Findings, Finding{
				Rule: RuleVersioning, Unit: path,
				Reason: fmt.Sprintf("s3://%s has no versioning, so a push couldn't be rolled back (S3-VER)", u.Bucket),
			})
		}
	}
	return nil
}

func (e *Engine) stateFile(path string, st *State) (string, error) {
	body, err := st.Marshal()
	if err != nil {
		return "", err
	}
	name := filepath.Join(e.tmp, safeName(path)+"."+strconv.FormatUint(st.Serial, 10)+".tfstate")
	if err = os.WriteFile(name, body, 0o600); err != nil {
		return "", fmt.Errorf("write state copy: %w", err)
	}
	return name, nil
}

// safeName turns a unit path into a file name that is valid on every OS: anything but letters,
// digits, '.', '-' and '_' becomes '_', and "/" becomes "__" so different paths stay different.
func safeName(path string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, strings.ReplaceAll(path, "/", "__"))
}

// verify plans every unit of a surgery block against its new state.
func (e *Engine) verify(ctx context.Context, b *Block, rep *BlockReport, p *pending) error {
	for _, path := range p.order {
		file, err := e.stateFile(path, p.states[path])
		if err != nil {
			return err
		}
		u := e.o.Units[path]
		e.o.Logger.InfoContext(ctx, "verifying", "block", b.Name, "unit", path)
		changes, err := e.o.Planner.Plan(ctx, u.Dir, file)
		if err != nil {
			if errors.Is(err, ErrLeftover) || ctx.Err() != nil {
				return err
			}
			rep.Findings = append(rep.Findings, Finding{Rule: RulePlan, Unit: path, Reason: err.Error()})
			continue
		}
		for _, c := range surgeryRejects(changes) {
			rep.Findings = append(rep.Findings, Finding{
				Rule:    RuleVerify,
				Unit:    path,
				Address: c.Address,
				Reason: "the plan would " + strings.Join(
					c.Actions,
					" and ",
				) + " it; a move may only leave no-op and read",
			})
		}
	}
	return nil
}

// Plan plans and verifies the blocks without writing to any backend.
func (e *Engine) Plan(ctx context.Context, f *File, blocks []*Block) (*Report, error) {
	rep := &Report{File: f.Name, Command: "plan", Blocks: []BlockReport{}}
	for _, b := range blocks {
		br, p, err := e.planBlock(ctx, f, b)
		if err != nil {
			return rep, err
		}
		rep.Blocks = append(rep.Blocks, *br)
		if p != nil {
			// Later blocks see this one's result.
			for path, st := range p.states {
				e.cur[path].state = st
			}
		}
	}
	return rep, nil
}

// Apply plans, verifies and applies each block in order, stopping at the first that can't be
// applied. A surgery block is confirmed through gate, then pushes the destinations before the
// source and records history in each unit. A native block writes its files.
func (e *Engine) Apply(ctx context.Context, f *File, blocks []*Block, gate prompt.Gate, force bool) (*Report, error) {
	rep := &Report{File: f.Name, Command: "apply", Blocks: []BlockReport{}}
	for _, b := range blocks {
		br, p, err := e.planBlock(ctx, f, b)
		if err != nil {
			return rep, err
		}
		rep.Blocks = append(rep.Blocks, *br)
		last := &rep.Blocks[len(rep.Blocks)-1]
		switch {
		case br.Status == StatusPartial:
			return rep, fmt.Errorf("%w: %s; run move rollback, or finish it by hand and run move mark-applied",
				ErrPartial, b.Name)
		case br.Status == StatusApplied:
			continue
		case len(br.Findings) > 0:
			return rep, fmt.Errorf("%w: block %s", ErrRejected, b.Name)
		case b.Strategy == StrategyNative:
			if err = e.applyNative(ctx, b, last, p, force); err != nil {
				return rep, err
			}
			continue
		}
		if err = gate.Confirm(ctx, confirmMessage(b, last), b.ConfirmPath()); err != nil {
			return rep, err
		}
		if err = e.push(ctx, f, b, last, p); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func confirmMessage(b *Block, rep *BlockReport) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Block %s pushes %d state(s):", b.Name, len(rep.Units))
	for _, u := range rep.Units {
		fmt.Fprintf(&sb, "\n  %s (%s, %d address(es))", u.Path, u.Role, len(u.Moved))
	}
	return sb.String()
}

func (e *Engine) push(ctx context.Context, f *File, b *Block, rep *BlockReport, p *pending) error {
	// Check every unit first, so a stale read stops the block before the first push.
	for _, path := range p.order {
		u := e.o.Units[path]
		c, err := e.client(ctx, u)
		if err != nil {
			return err
		}
		if err = checkUnchanged(ctx, c.Reader, u, p.snaps[path]); err != nil {
			return err
		}
		// Rollback treats an empty version as "no state before", so an unversioned read must stop here.
		if p.snaps[path].exists && p.snaps[path].versionID == "" {
			return fmt.Errorf("%w: %s was read without a version ID, so a push couldn't be rolled back",
				ErrVersioning, u.location())
		}
	}
	var pushed []string
	for _, path := range p.order {
		if err := e.pushUnit(ctx, f, b, rep, p, path); err != nil {
			if len(pushed) > 0 {
				rep.Result = ResultPartial
				return fmt.Errorf("%w; already pushed: %s; run tfcensus move rollback %s --block %s",
					err, strings.Join(pushed, ", "), f.Name, b.Name)
			}
			return err
		}
		pushed = append(pushed, path)
	}
	rep.Result, rep.Status = ResultApplied, StatusApplied
	return nil
}

func (e *Engine) pushUnit(ctx context.Context, f *File, b *Block, rep *BlockReport, p *pending, path string) error {
	u := e.o.Units[path]
	c, err := e.client(ctx, u)
	if err != nil {
		return err
	}
	snap := p.snaps[path]
	if err = checkUnchanged(ctx, c.Reader, u, snap); err != nil {
		return err
	}
	file, err := e.stateFile(path, p.states[path])
	if err != nil {
		return err
	}
	e.o.Logger.InfoContext(ctx, "pushing state", "block", b.Name, "unit", path, "location", u.location())
	if err = e.o.Pusher.Push(ctx, u.Dir, file); err != nil {
		return fmt.Errorf("push %s: %w", path, err)
	}
	after, err := pull(ctx, c.Reader, u)
	if err != nil {
		return err
	}
	st, err := ParseState(after.body)
	if err != nil {
		return fmt.Errorf("%s after the push: %w", u.location(), err)
	}
	e.cur[path] = &current{snap: after, state: st}
	ids := &VersionIDs{After: after.versionID}
	if snap.exists {
		ids.Before = snap.versionID
	}
	rep.unit(path).VersionIDs = ids
	return e.record(ctx, f, b, path, Record{VersionIDs: ids})
}

// record appends a record for the block to the unit's history.
func (e *Engine) record(ctx context.Context, f *File, b *Block, path string, r Record) error {
	u := e.o.Units[path]
	c, err := e.client(ctx, u)
	if err != nil {
		return err
	}
	// Read again: another run may have written since this one started.
	delete(e.hist, path)
	he, err := e.history(ctx, path)
	if err != nil {
		return err
	}
	r.File, r.Block, r.Role, r.Strategy = f.Name, b.Name, roleOf(b, path), b.Strategy
	r.AppliedAt, r.By = e.o.Clock().UTC(), c.By
	he.h.Records = append(he.h.Records, r)
	e.o.Logger.InfoContext(ctx, "writing history", "unit", path, "location", u.location()+HistorySuffix)
	if err = writeHistory(ctx, c.Writer, u, he.h, he.etag); err != nil {
		return err
	}
	delete(e.hist, path)
	return nil
}

func (e *Engine) applyNative(ctx context.Context, b *Block, rep *BlockReport, p *pending, force bool) error {
	for _, path := range p.order {
		e.o.Logger.InfoContext(ctx, "writing file", "unit", path, "file", NativeFileName(b.Name))
		if err := writeNative(e.o.Units[path].Dir, b.Name, p.files[path], force); err != nil {
			return err
		}
	}
	rep.Result = ResultWritten
	for _, path := range p.order {
		ur := rep.unit(path)
		changes, err := e.o.Planner.Plan(ctx, e.o.Units[path].Dir, "")
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			rep.Findings = append(rep.Findings, Finding{Rule: RulePlan, Unit: path, Reason: err.Error()})
			continue
		}
		for _, c := range nativeRejects(changes, ur.Role, ur.Moved) {
			reason := "the plan would " + strings.Join(c.Actions, " and ") + " it"
			if len(c.Actions) == 0 {
				reason = "the plan doesn't mention it; move its configuration into the unit"
			}
			rep.Findings = append(
				rep.Findings,
				Finding{Rule: RuleVerify, Unit: path, Address: c.Address, Reason: reason},
			)
		}
	}
	if len(rep.Findings) > 0 {
		return fmt.Errorf("%w: block %s (the files stay written)", ErrRejected, b.Name)
	}
	return nil
}

// Status reports each block's status in every unit it touches.
func (e *Engine) Status(ctx context.Context, f *File, blocks []*Block) (*Report, error) {
	rep := &Report{File: f.Name, Command: "status", Blocks: []BlockReport{}}
	for _, b := range blocks {
		status, recs, err := e.status(ctx, f, b)
		if err != nil {
			return rep, err
		}
		br := newBlockReport(b, status)
		for i := range br.Units {
			br.Units[i].Status = StatusPending
			if r := recs[br.Units[i].Path]; r != nil {
				br.Units[i].Status, br.Units[i].VersionIDs = StatusApplied, r.VersionIDs
			}
		}
		rep.Blocks = append(rep.Blocks, *br)
	}
	return rep, nil
}

// MarkApplied records blocks as applied without changing any state, for migrations done by hand
// or through the native strategy.
func (e *Engine) MarkApplied(ctx context.Context, f *File, blocks []*Block, gate prompt.Gate) (*Report, error) {
	rep := &Report{File: f.Name, Command: "mark-applied", Blocks: []BlockReport{}}
	for _, b := range blocks {
		status, recs, err := e.status(ctx, f, b)
		if err != nil {
			return rep, err
		}
		br := newBlockReport(b, status)
		rep.Blocks = append(rep.Blocks, *br)
		last := &rep.Blocks[len(rep.Blocks)-1]
		if status == StatusApplied {
			last.Result = ResultSkipped
			continue
		}
		msg := fmt.Sprintf(
			"Record block %s as applied in %d unit(s) without changing any state.",
			b.Name,
			len(b.Units()),
		)
		if err = gate.Confirm(ctx, msg, b.ConfirmPath()); err != nil {
			return rep, err
		}
		for _, path := range b.Units() {
			if recs[path] != nil {
				continue
			}
			if err = e.record(ctx, f, b, path, Record{Manual: true}); err != nil {
				return rep, err
			}
		}
		last.Result, last.Status = ResultMarked, StatusApplied
	}
	return rep, nil
}

// Rollback restores every unit of each applied block to the state version it had before the
// push, byte for byte, in reverse block order and with the source first. A unit whose state
// changed since the push stops the rollback unless force is set.
func (e *Engine) Rollback(
	ctx context.Context,
	f *File,
	blocks []*Block,
	gate prompt.Gate,
	force bool,
) (*Report, error) {
	rep := &Report{File: f.Name, Command: "rollback", Blocks: []BlockReport{}}
	for _, b := range slices.Backward(blocks) {
		br, err := e.rollbackBlock(ctx, f, b, gate, force)
		if br != nil {
			rep.Blocks = append(rep.Blocks, *br)
		}
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (e *Engine) rollbackBlock(
	ctx context.Context,
	f *File,
	b *Block,
	gate prompt.Gate,
	force bool,
) (*BlockReport, error) {
	status, recs, err := e.status(ctx, f, b)
	if err != nil {
		return nil, err
	}
	br := newBlockReport(b, status)
	if status == StatusPending {
		br.Result = ResultSkipped
		return br, nil
	}
	order := rollbackOrder(b, recs)
	if err = e.checkRollback(ctx, b, order, recs, force); err != nil {
		return br, err
	}
	msg := fmt.Sprintf("Block %s restores %d state(s) to their versions before the push: %s",
		b.Name, len(order), strings.Join(order, ", "))
	if err = gate.Confirm(ctx, msg, b.ConfirmPath()); err != nil {
		return br, err
	}
	for _, path := range order {
		if err = e.restoreUnit(ctx, b, path, recs[path]); err != nil {
			return br, err
		}
		br.unit(path).VersionIDs = recs[path].VersionIDs
	}
	br.Result, br.Status = ResultRolledBack, StatusPending
	return br, nil
}

// rollbackOrder lists the units with a record, source first and then the destinations in
// reverse, the opposite of the push order.
func rollbackOrder(b *Block, recs map[string]*Record) []string {
	order := []string{b.From}
	for _, d := range slices.Backward(b.To) {
		order = append(order, d.Unit)
	}
	return slices.DeleteFunc(order, func(p string) bool { return recs[p] == nil })
}

// checkRollback fails for records tfcensus can't restore and, without force, for states that
// changed after the push.
func (e *Engine) checkRollback(
	ctx context.Context,
	b *Block,
	order []string,
	recs map[string]*Record,
	force bool,
) error {
	for _, path := range order {
		r := recs[path]
		if r.Manual || r.VersionIDs == nil {
			return fmt.Errorf("%w: %s in %s; restore it by hand", ErrManual, b.Name, path)
		}
		u := e.o.Units[path]
		c, err := e.client(ctx, u)
		if err != nil {
			return err
		}
		v, exists, err := head(ctx, c.Reader, u)
		if err != nil {
			return err
		}
		if (!exists || v != r.VersionIDs.After) && !force {
			return fmt.Errorf("%w: %s is at version %q, but the push wrote %q; check what changed, then pass --force",
				ErrChanged, path, v, r.VersionIDs.After)
		}
	}
	return nil
}

func (e *Engine) restoreUnit(ctx context.Context, b *Block, path string, r *Record) error {
	u := e.o.Units[path]
	c, err := e.client(ctx, u)
	if err != nil {
		return err
	}
	info, err := newLockInfo(u, "move rollback", e.o.Clock())
	if err != nil {
		return err
	}
	if u.Lock == discovery.LockNone || u.Lock == "" {
		e.o.Logger.WarnContext(ctx, "the backend has no locking; restoring without a lock", "unit", path)
	}
	unlock, err := lock(ctx, c, u, info)
	if err != nil {
		return err
	}
	e.o.Logger.InfoContext(ctx, "restoring state", "block", b.Name, "unit", path, "location", u.location(),
		"version", r.VersionIDs.Before)
	err = restore(ctx, c, u, r.VersionIDs.Before)
	if unlockErr := unlock(context.WithoutCancel(ctx)); unlockErr != nil {
		err = errors.Join(err, unlockErr)
	}
	if err != nil {
		return err
	}
	delete(e.cur, path)
	delete(e.hist, path)
	he, err := e.history(ctx, path)
	if err != nil {
		return err
	}
	now := e.o.Clock().UTC()
	for i := range he.h.Records {
		hr := &he.h.Records[i]
		if hr.File == r.File && hr.Block == r.Block && hr.RolledBackAt == nil {
			hr.RolledBackAt = &now
		}
	}
	e.o.Logger.InfoContext(ctx, "writing history", "unit", path, "location", u.location()+HistorySuffix)
	if err = writeHistory(ctx, c.Writer, u, he.h, he.etag); err != nil {
		return err
	}
	delete(e.hist, path)
	return nil
}
