package tfstate

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/discovery"
)

// DefaultConcurrency is how many states [Reader] reads at once.
const DefaultConcurrency = 16

// Status is the outcome of reading one unit's state.
type Status string

// Read outcomes.
const (
	StatusOK          Status = "ok"
	StatusAbsent      Status = "absent"      // the key doesn't exist: the unit was never applied
	StatusEncrypted   Status = "encrypted"   // OpenTofu client-side encryption
	StatusUnsupported Status = "unsupported" // a state format other than version 4
	StatusError       Status = "error"
	StatusSkipped     Status = "skipped" // no s3 backend to read
)

// Engines, reported only when something in the unit or its state shows which one applied it.
const (
	EngineTerraform = "terraform"
	EngineOpenTofu  = "opentofu"
)

// VersionFromLockFile says a [ProviderVersion] came from the unit's dependency lock file.
const VersionFromLockFile = "lockfile"

// UnitState is the inventory of one unit and workspace. It holds identifiers and counts, never
// attribute values.
type UnitState struct {
	Repo             string            `json:"repo"`
	Path             string            `json:"path"`
	Workspace        string            `json:"workspace"`
	Bucket           string            `json:"bucket,omitempty"`
	Key              string            `json:"key,omitempty"`
	Region           string            `json:"region,omitempty"`
	Status           Status            `json:"status"`
	Engine           string            `json:"engine,omitempty"`
	EngineFrom       string            `json:"engine_from,omitempty"`
	StateVersion     int               `json:"state_version,omitempty"`
	TerraformVersion string            `json:"terraform_version,omitempty"`
	Serial           *uint64           `json:"serial,omitempty"`
	Lineage          string            `json:"lineage,omitempty"`
	Encrypted        bool              `json:"encrypted"`
	Size             int64             `json:"size,omitempty"`
	LastModified     *time.Time        `json:"last_modified,omitempty"`
	Providers        []ProviderVersion `json:"providers"`
	Resources        int               `json:"resources"`    // managed resource blocks
	Instances        int               `json:"instances"`    // managed resource instances
	DataSources      int               `json:"data_sources"` // data resource blocks
	ResourceTypes    map[string]int    `json:"resource_types"`
	Error            string            `json:"error,omitempty"`
	Warnings         []string          `json:"warnings"`

	Target discovery.Target `json:"-"`
	State  *State           `json:"-"`
	Err    error            `json:"-"`
}

// ProviderVersion is a provider a state uses. Version comes from the unit's lock file, because
// raw state records provider addresses but not their versions.
type ProviderVersion struct {
	Source        string   `json:"source"`
	Aliases       []string `json:"aliases,omitempty"`
	Version       string   `json:"version,omitempty"`
	VersionSource string   `json:"version_source,omitempty"`
}

// Unit returns repo/path.
func (u *UnitState) Unit() string { return u.Repo + "/" + u.Path }

// Options configures a [Reader].
type Options struct {
	// Client returns the S3 client that reads u's state, with the backend's credentials.
	Client      func(ctx context.Context, u *discovery.Unit) (Client, error)
	WorkDir     string // resolves relative repo paths, to find lock and version files
	Concurrency int    // 0 means [DefaultConcurrency]
	TFPath      string // the terraform binary terragrunt runs; a tofu binary marks terragrunt units as OpenTofu
	Logger      *slog.Logger
	Parse       ParseOptions // what to keep beyond identifiers, for matching
}

// Reader reads the state of many units concurrently.
type Reader struct {
	opts Options

	mu       sync.Mutex
	listings map[string]*listing
}

type listing struct {
	once sync.Once
	keys []string
	err  error
}

// NewReader returns a Reader.
func NewReader(opts Options) *Reader {
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Reader{opts: opts, listings: map[string]*listing{}}
}

// Read reads the state of every unit, and of each unit's other workspaces. A unit that fails
// gets [StatusError] and doesn't stop the others. Stacks are left out. The result is sorted by
// repo, path and workspace.
func (r *Reader) Read(ctx context.Context, units []discovery.Unit) []UnitState {
	results := make([][]UnitState, len(units))
	g := new(errgroup.Group)
	g.SetLimit(r.opts.Concurrency)
	for i := range units {
		if units[i].Kind == discovery.KindTerragruntStack {
			continue
		}
		g.Go(func() error {
			results[i] = r.unit(ctx, &units[i])
			return nil
		})
	}
	_ = g.Wait() // failures are per unit, in the results
	var out []UnitState
	for _, rs := range results {
		out = append(out, rs...)
	}
	slices.SortStableFunc(out, func(a, b UnitState) int {
		return cmp.Or(strings.Compare(a.Repo, b.Repo), strings.Compare(a.Path, b.Path),
			workspaceOrder(a.Workspace, b.Workspace))
	})
	return out
}

func workspaceOrder(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == DefaultWorkspace:
		return -1
	case b == DefaultWorkspace:
		return 1
	}
	return strings.Compare(a, b)
}

func (r *Reader) unit(ctx context.Context, u *discovery.Unit) []UnitState {
	base := UnitState{
		Repo: u.Repo, Path: u.Path, Workspace: DefaultWorkspace, Target: u.Target,
		Providers: []ProviderVersion{}, ResourceTypes: map[string]int{}, Warnings: []string{},
	}
	b := u.Backend
	switch {
	case b == nil:
		base.Status = StatusSkipped
		base.Warnings = append(base.Warnings, "backend unresolved: "+u.Reason)
		return []UnitState{base}
	case b.Type != discovery.BackendS3:
		base.Status = StatusSkipped
		base.Warnings = append(base.Warnings, "backend "+b.Type+": tfcensus reads state from s3 only")
		return []UnitState{base}
	}
	base.Bucket, base.Key, base.Region = b.Bucket, b.Key, b.Region
	client, err := r.opts.Client(ctx, u)
	if err != nil {
		base.fail(err)
		return []UnitState{base}
	}
	workspaces := []string{DefaultWorkspace}
	others, err := r.workspaces(ctx, client, b)
	if err != nil {
		base.Warnings = append(base.Warnings, "workspaces not listed: "+err.Error())
	}
	workspaces = append(workspaces, others...)

	dir := r.unitDir(u)
	versions, lockErr := ReadLockFile(dir)
	out := make([]UnitState, 0, len(workspaces))
	for _, ws := range workspaces {
		us := base
		us.Warnings = slices.Clone(base.Warnings)
		us.Providers, us.ResourceTypes = []ProviderVersion{}, map[string]int{}
		us.Workspace = ws
		us.Key = WorkspaceKey(b.WorkspaceKeyPrefix, b.Key, ws)
		if lockErr != nil {
			us.Warnings = append(us.Warnings, lockErr.Error())
		}
		r.read(ctx, client, &us, versions)
		us.Engine, us.EngineFrom = r.engine(u, dir, us.State)
		r.opts.Logger.DebugContext(ctx, "read state", "unit", us.Unit(), "workspace", ws, "status", string(us.Status))
		out = append(out, us)
	}
	return out
}

func (r *Reader) read(ctx context.Context, c Client, us *UnitState, versions map[string]string) {
	obj, err := Fetch(ctx, c, us.Bucket, us.Key, r.opts.Parse)
	if obj != nil {
		us.Size, us.LastModified = obj.Size, obj.LastModified
	}
	switch {
	case errors.Is(err, ErrAbsent):
		us.Status = StatusAbsent
		return
	case errors.Is(err, ErrUnsupportedVersion):
		us.Status = StatusUnsupported
		us.StateVersion, us.TerraformVersion, us.Lineage = obj.State.Version, obj.State.TerraformVersion, obj.State.Lineage
		us.Serial = &obj.State.Serial
		us.Warnings = append(us.Warnings, err.Error())
		return
	case err != nil:
		us.fail(err)
		return
	}
	s := obj.State
	us.State = s
	us.StateVersion, us.TerraformVersion, us.Lineage = s.Version, s.TerraformVersion, s.Lineage
	us.Serial = &s.Serial
	if s.Encrypted {
		us.Status, us.Encrypted = StatusEncrypted, true
		us.Warnings = append(
			us.Warnings,
			"state is encrypted with OpenTofu client-side encryption, so only serial and lineage "+
				"are readable; tofu show -json in the unit reads it with the unit's key configuration",
		)
		return
	}
	us.Status = StatusOK
	us.summarize(s, versions)
}

func (u *UnitState) fail(err error) {
	u.Status = StatusError
	u.Error = err.Error()
	u.Err = err
}

func (u *UnitState) summarize(s *State, versions map[string]string) {
	aliases := map[string][]string{}
	for _, res := range s.Resources {
		if _, ok := aliases[res.Provider.Source]; !ok {
			aliases[res.Provider.Source] = nil
		}
		if res.Provider.Alias != "" && !slices.Contains(aliases[res.Provider.Source], res.Provider.Alias) {
			aliases[res.Provider.Source] = append(aliases[res.Provider.Source], res.Provider.Alias)
		}
		if res.Mode == ModeData {
			u.DataSources++
			continue
		}
		u.Resources++
		for _, inst := range res.Instances {
			if inst.Deposed != "" {
				continue
			}
			u.Instances++
			u.ResourceTypes[res.Type]++
		}
	}
	for _, src := range slices.Sorted(maps.Keys(aliases)) {
		pv := ProviderVersion{Source: src, Aliases: aliases[src]}
		slices.Sort(pv.Aliases)
		if v := versions[src]; v != "" {
			pv.Version, pv.VersionSource = v, VersionFromLockFile
		}
		u.Providers = append(u.Providers, pv)
	}
}

func (r *Reader) unitDir(u *discovery.Unit) string {
	return filepath.Join(r.repoDir(u), filepath.FromSlash(u.Path))
}

// workspaces lists the non-default workspaces of b. Units in one bucket share one listing.
func (r *Reader) workspaces(ctx context.Context, c Client, b *discovery.Backend) ([]string, error) {
	prefix := workspacePrefix(b.WorkspaceKeyPrefix) + "/"
	r.mu.Lock()
	l, ok := r.listings[b.Bucket+"\x00"+prefix]
	if !ok {
		l = &listing{}
		r.listings[b.Bucket+"\x00"+prefix] = l
	}
	r.mu.Unlock()
	l.once.Do(func() { l.keys, l.err = listKeys(ctx, c, b.Bucket, prefix) })
	if l.err != nil {
		return nil, l.err
	}
	return workspacesIn(l.keys, b.WorkspaceKeyPrefix, b.Key), nil
}

// engine says which tool applied the state, when the state or the unit shows it.
func (r *Reader) engine(u *discovery.Unit, dir string, s *State) (string, string) {
	if s != nil {
		if s.Encrypted {
			return EngineOpenTofu, "state encryption"
		}
		for _, res := range s.Resources {
			if res.Provider.OpenTofu() {
				return EngineOpenTofu, "state provider registry"
			}
		}
	}
	if e, f := versionFile(dir, r.repoDir(u)); e != "" {
		return e, f
	}
	if u.Kind == discovery.KindTerragrunt && strings.HasPrefix(filepath.Base(r.opts.TFPath), "tofu") {
		return EngineOpenTofu, "--tf-path"
	}
	return "", ""
}

func (r *Reader) repoDir(u *discovery.Unit) string {
	repo := filepath.FromSlash(u.Repo)
	if !filepath.IsAbs(repo) {
		repo = filepath.Join(r.opts.WorkDir, repo)
	}
	return repo
}

// versionFile looks for a version manager file from dir up to root.
func versionFile(dir, root string) (string, string) {
	for {
		for _, f := range []struct{ name, engine string }{
			{".opentofu-version", EngineOpenTofu},
			{".terraform-version", EngineTerraform},
		} {
			if _, err := os.Stat(filepath.Join(dir, f.name)); err == nil {
				return f.engine, f.name
			}
		}
		if dir == root || filepath.Dir(dir) == dir || !strings.HasPrefix(dir, root) {
			return "", ""
		}
		dir = filepath.Dir(dir)
	}
}
