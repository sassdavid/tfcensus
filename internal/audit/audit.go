package audit

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// DefaultConcurrency is how many buckets and lock tables are audited at once.
const DefaultConcurrency = 8

// Bucket sources.
const (
	SourceUnit     = "unit"     // a unit's backend names the bucket
	SourceDiscover = "discover" // found by --discover
)

// Options configures [Run].
type Options struct {
	// Credentials returns the account that reads a backend and the region to call it in.
	Credentials func(ctx context.Context, ref accounts.BackendRef) (*accounts.Account, string, error)
	// S3 returns the S3 client for an account and region.
	S3 func(acct *accounts.Account, region string) *s3.Client
	// Discover lists bucket name globs. When set, every bucket in Accounts and in the accounts
	// that hold unit backends is listed, and buckets matching a glob are audited too.
	Discover    []string
	Accounts    []*accounts.Account
	Policy      config.AuditPolicy
	Suppress    *Suppressions
	Now         time.Time
	Concurrency int // 0 means [DefaultConcurrency]
	Logger      *slog.Logger
}

// Input is what discovery and the state reader found.
type Input struct {
	Units  []discovery.Unit
	States []tfstate.UnitState
	Index  *tfstate.Index
}

// Report is the audit result. Findings and Suppressed are sorted by severity, check and target.
type Report struct {
	Checks     []Check     `json:"checks"`
	Buckets    []Bucket    `json:"buckets"`
	LockTables []LockTable `json:"lock_tables"`
	Findings   []Finding   `json:"findings"`
	Suppressed []Finding   `json:"suppressed"`
	Warnings   []string    `json:"warnings"`
	Incomplete []Problem   `json:"incomplete"` // checks that could not run
}

// Bucket is what the audit learned about a state bucket.
type Bucket struct {
	Name        string    `json:"name"`
	Account     string    `json:"account,omitempty"`
	Region      string    `json:"region,omitempty"`
	Source      string    `json:"source"`
	Units       int       `json:"units"`      // units whose backend names the bucket
	StateKeys   int       `json:"state_keys"` // objects ending in .tfstate
	Versioning  string    `json:"versioning,omitempty"`
	Encryption  string    `json:"encryption,omitempty"`
	Replication []Replica `json:"replication"`
	ReplicaOf   string    `json:"replica_of,omitempty"` // an audited bucket that replicates to this one
}

// Replica is a replication destination of a bucket.
type Replica struct {
	Bucket string `json:"bucket"`
	Region string `json:"region,omitempty"`
	Status string `json:"status"` // the rule's status: Enabled or Disabled
}

// LockTable is what the audit learned about a DynamoDB lock table.
type LockTable struct {
	Name       string `json:"name"`
	Account    string `json:"account,omitempty"`
	Region     string `json:"region,omitempty"`
	Units      int    `json:"units"`
	PITR       *bool  `json:"pitr,omitempty"`
	Encryption string `json:"encryption,omitempty"` // KMS, or "AWS owned key"
	Locks      int    `json:"locks"`                // lock items, digests excluded
}

// Problem is a check that could not run on a target.
type Problem struct {
	Target  string `json:"target"`
	Check   string `json:"check,omitempty"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

// Run audits the backends of in.Units, their lock tables and the fleet of states. API errors
// become [Problem]s; Run itself only fails when ctx is cancelled.
func Run(ctx context.Context, opts Options, in Input) (*Report, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	a := &auditor{opts: opts, accountPAB: map[string]*memo[pabResult]{}, trails: map[string]*memo[trailResult]{}}
	rep := &Report{Checks: Checks(), Buckets: []Bucket{}, LockTables: []LockTable{}}
	buckets := bucketTargets(in)
	if len(opts.Discover) > 0 {
		buckets = append(buckets, a.discover(ctx, buckets, rep)...)
	}
	results := make([]bucketResult, len(buckets))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)
	for i := range buckets {
		g.Go(func() error {
			results[i] = a.auditBucket(gctx, buckets[i])
			return nil
		})
	}
	tables := tableTargets(in)
	tableResults := make([]tableResult, len(tables))
	_ = g.Wait()
	listings := map[string]map[string]bool{}
	for i := range results {
		listings[buckets[i].name] = results[i].keys
	}
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)
	for i := range tables {
		g.Go(func() error {
			tableResults[i] = a.auditTable(gctx, tables[i], listings)
			return nil
		})
	}
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err //nolint:wrapcheck // cancellation passes through unchanged
	}
	markReplicas(buckets, results)
	var findings []Finding
	for i := range results {
		rep.Buckets = append(rep.Buckets, results[i].bucket)
		findings = append(findings, results[i].findings...)
		rep.Incomplete = append(rep.Incomplete, results[i].problems...)
	}
	for i := range tableResults {
		rep.LockTables = append(rep.LockTables, tableResults[i].table)
		findings = append(findings, tableResults[i].findings...)
		rep.Incomplete = append(rep.Incomplete, tableResults[i].problems...)
	}
	findings = append(findings, lockModeFindings(in.Units)...)
	findings = append(findings, fleetFindings(in, buckets, results, opts.Policy)...)
	rep.finish(findings, opts)
	return rep, nil
}

// markReplicas finds audited buckets that another audited bucket replicates to. A replica needs no
// replication of its own, and the state copies in it belong to the source's units.
func markReplicas(buckets []*bucketTarget, results []bucketResult) {
	byName := map[string]int{}
	for i, t := range buckets {
		byName[t.name] = i
	}
	for i := range results {
		for _, rep := range results[i].bucket.Replication {
			j, ok := byName[rep.Bucket]
			if !ok || j == i || rep.Status != string(types.ReplicationRuleStatusEnabled) {
				continue
			}
			results[j].bucket.ReplicaOf = buckets[i].name
			for k := range buckets[i].known {
				buckets[j].known[k] = true
			}
			if results[j].noReplication {
				results[j].findings = slices.DeleteFunc(results[j].findings, func(f Finding) bool {
					return f.Rule == CheckS3Replication
				})
			}
		}
	}
}

func (r *Report) finish(findings []Finding, opts Options) {
	if opts.Suppress != nil {
		r.Findings, r.Suppressed, r.Warnings = opts.Suppress.Apply(findings, opts.Now)
	} else {
		r.Findings = findings
	}
	sortFindings(r.Findings)
	sortFindings(r.Suppressed)
	for _, s := range []*[]Finding{&r.Findings, &r.Suppressed} {
		if *s == nil {
			*s = []Finding{}
		}
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	slices.SortStableFunc(r.Incomplete, func(a, b Problem) int {
		return cmp.Or(strings.Compare(a.Target, b.Target), strings.Compare(a.Check, b.Check))
	})
	if r.Incomplete == nil {
		r.Incomplete = []Problem{}
	}
}

type auditor struct {
	opts Options

	mu         sync.Mutex
	accountPAB map[string]*memo[pabResult]
	trails     map[string]*memo[trailResult]
}

type memo[T any] struct {
	once sync.Once
	val  T
}

func cached[T any](a *auditor, m map[string]*memo[T], key string, load func() T) T {
	a.mu.Lock()
	e, ok := m[key]
	if !ok {
		e = &memo[T]{}
		m[key] = e
	}
	a.mu.Unlock()
	e.once.Do(func() { e.val = load() })
	return e.val
}

// bucketTarget is one bucket to audit.
type bucketTarget struct {
	name   string
	ref    accounts.BackendRef
	acct   *accounts.Account // set for discovered buckets
	region string
	source string
	units  []string
	known  map[string]bool // state keys that units resolve to
}

// bucketTargets groups the s3 units by bucket. The first unit (by repo and path) gives the
// credentials; every unit's key and every state read under the bucket counts as known.
func bucketTargets(in Input) []*bucketTarget {
	byName := map[string]*bucketTarget{}
	var out []*bucketTarget
	for i := range in.Units {
		u := &in.Units[i]
		if u.Excluded || u.Backend == nil || u.Backend.Type != discovery.BackendS3 {
			continue
		}
		t := byName[u.Backend.Bucket]
		if t == nil {
			t = &bucketTarget{
				name: u.Backend.Bucket, ref: unitRef(u), region: cmp.Or(u.Backend.Region, u.Target.Region),
				source: SourceUnit, known: map[string]bool{},
			}
			byName[t.name] = t
			out = append(out, t)
		}
		t.units = append(t.units, u.Repo+"/"+u.Path)
		t.known[u.Backend.Key] = true
	}
	for i := range in.States {
		s := &in.States[i]
		if t := byName[s.Bucket]; t != nil && s.Key != "" {
			t.known[s.Key] = true
		}
	}
	slices.SortFunc(out, func(a, b *bucketTarget) int { return strings.Compare(a.name, b.name) })
	return out
}

func unitRef(u *discovery.Unit) accounts.BackendRef {
	return accounts.BackendRef{
		Profile: u.Backend.Profile, RoleARN: u.Backend.RoleARN, Account: u.Target.Account,
		Region: cmp.Or(u.Backend.Region, u.Target.Region),
	}
}

// credentials returns the account and region for a target.
func (a *auditor) credentials(ctx context.Context, t *bucketTarget) (*accounts.Account, string, error) {
	if t.acct != nil {
		return t.acct, t.region, nil
	}
	acct, region, err := a.opts.Credentials(ctx, t.ref)
	if err != nil {
		return nil, "", err
	}
	return acct, cmp.Or(t.region, region), nil
}

// apiCode returns the AWS error code of err, or "".
func apiCode(err error) string {
	if ae, ok := errors.AsType[smithy.APIError](err); ok {
		return ae.ErrorCode()
	}
	return ""
}

// problem turns an API error into a Problem with a short message.
func problem(target, check string, err error) Problem {
	msg := err.Error()
	if code := apiCode(err); code != "" {
		msg = code
		if ae, ok := errors.AsType[smithy.APIError](err); ok && ae.ErrorMessage() != "" {
			msg += ": " + ae.ErrorMessage()
		}
	}
	return Problem{Target: target, Check: check, Message: msg, Err: err}
}
