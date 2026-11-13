package cli

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/report"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
	"github.com/sassdavid/tfcensus/internal/whois"
)

// errNoSource says which flag a saved-report question needs.
var errNoSource = errors.New("no saved report")

// saved answers questions from files earlier runs wrote: the state index, a scan JSON or the scan
// cache, and audit and drift JSON. It starts no process, calls no AWS API and writes nothing, so
// tfcensus mcp can use it.
type saved struct {
	d     *app.Deps
	cfg   *config.Config
	index string // inventory --save-index
	scan  string // scan --format json; empty means the newest scan cache snapshot
	audit string // audit --format json
	drift string // drift --format json
}

// result is an envelope's data and errors.
type result struct {
	data any
	errs []render.Error
}

func (s *saved) need(path, flag, cmd string) error {
	if path == "" {
		return usageError(fmt.Errorf("%w: pass --%s with the output of %s", errNoSource, flag, cmd))
	}
	return nil
}

func (s *saved) read(path string, want report.Kind) (*report.Saved, error) {
	sv, err := report.Read(path)
	if err != nil {
		return nil, usageError(err)
	}
	if sv.Kind != want {
		return nil, usageError(fmt.Errorf("%s is a %s envelope, want %s", path, sv.Kind, want))
	}
	return sv, nil
}

func (s *saved) readIndex() (*tfstate.Index, error) {
	if err := s.need(s.index, flagIndex, "tfcensus inventory --save-index"); err != nil {
		return nil, err
	}
	sv, err := s.read(s.index, report.KindIndex)
	if err != nil {
		return nil, err
	}
	return sv.Index, nil
}

func (s *saved) units(f report.UnitsFilter) (*result, error) {
	ix, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	return &result{data: report.SummarizeUnits(ix, f)}, nil
}

func (s *saved) inventory(f report.InventoryFilter) (*result, error) {
	ix, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	return &result{data: report.SummarizeInventory(ix, f)}, nil
}

// whois resolves queries against the saved index with the config's ownership rules, as
// tfcensus whois --index does without accounts or --live.
func (s *saved) whois(queries, types []string) (*result, error) {
	ix, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	m, err := typemap.Load()
	if err != nil {
		return nil, fmt.Errorf("load typemap: %w", err)
	}
	inState := &stateLookup{}
	eng, err := ownershipEngine(s.d, s.cfg, nil, inState.has)
	if err != nil {
		return nil, err
	}
	inState.index(ix)
	rv := &whois.Resolver{Matcher: match.New(m, ix), Map: m, Engine: eng}
	rep := &whoisReport{Answers: make([]whois.Answer, 0, len(queries)), Warnings: []string{}}
	for _, v := range queries {
		q := match.Query{Value: v, Types: types}
		rep.Answers = append(rep.Answers, rv.Resolve(&q))
	}
	rep.Warnings = append(rep.Warnings, expiredWarnings(eng)...)
	return &result{data: rep}, nil
}

func expiredWarnings(eng *ownership.Engine) []string {
	out := []string{}
	for _, name := range eng.Expired() {
		out = append(out, fmt.Sprintf("rule %q is past its expires date and no longer matches", name))
	}
	return out
}

func (s *saved) scanSummary(f report.ScanFilter) (*result, error) {
	if err := report.CheckScanFilter(&f); err != nil {
		return nil, usageError(err)
	}
	if s.scan != "" {
		sv, err := s.read(s.scan, report.KindScan)
		if err != nil {
			return nil, err
		}
		src := report.Source{File: filepath.Base(s.scan), Created: sv.GeneratedAt}
		return &result{data: report.SummarizeScan(sv.Scan, src, f), errs: sv.Errors}, nil
	}
	rep, src, errs, err := s.cachedScan()
	if err != nil {
		return nil, err
	}
	return &result{data: report.SummarizeScan(rep, src, f), errs: errs}, nil
}

// cachedScan classifies the newest scan cache snapshot with the config's rules. A stale snapshot
// is still used, with a warning, because collecting again needs AWS calls.
func (s *saved) cachedScan() (*scan.Report, report.Source, []render.Error, error) {
	dir, err := config.CacheDirPath(s.d.Env.Lookup, runtime.GOOS)
	if err != nil {
		return nil, report.Source{}, nil, fmt.Errorf("find cache directory: %w", err)
	}
	ttl, err := time.ParseDuration(cmp.Or(s.cfg.Cache.TTL, "1h"))
	if err != nil {
		return nil, report.Source{}, nil, usageError(fmt.Errorf("cache.ttl %q: %w", s.cfg.Cache.TTL, err))
	}
	snap, stale, err := (&scan.Cache{Dir: dir, TTL: ttl, Now: s.d.Clock}).Newest()
	if err != nil {
		return nil, report.Source{}, nil, usageError(fmt.Errorf("%w; or pass --scan with the output of "+
			"tfcensus scan --format json", err))
	}
	m, err := typemap.Load()
	if err != nil {
		return nil, report.Source{}, nil, fmt.Errorf("load typemap: %w", err)
	}
	inState := &stateLookup{}
	eng, err := ownershipEngine(s.d, s.cfg, nil, inState.has)
	if err != nil {
		return nil, report.Source{}, nil, err
	}
	inState.index(snap.Index)
	results := match.New(m, snap.Index).Match(snap.Inventory.Resources)
	findings := scan.Classify(results, eng, m, ownership.NewIndex(snap.Inventory.Resources))
	sortFindings(findings)
	rep := &scan.Report{
		Findings: findings, Runs: snap.Inventory.Runs, Coverage: snap.Inventory.Coverage,
		Warnings: expiredWarnings(eng), Cached: true,
	}
	rep.Regions, rep.Accounts = scan.Summarize(findings)
	src := report.Source{Cache: true, Created: snap.Created.UTC().Truncate(time.Second), Stale: stale}
	if stale {
		rep.Warnings = append(
			rep.Warnings,
			fmt.Sprintf("the newest scan snapshot is from %s, older than cache.ttl %s; "+
				"run tfcensus scan to collect again", src.Created.Format(time.RFC3339), ttl),
		)
	}
	errs := []render.Error{}
	for _, p := range snap.Problems {
		errs = append(errs, render.Error{Target: p.Target, Message: p.Message})
	}
	return rep, src, errs, nil
}

func (s *saved) auditFindings(f report.AuditFilter) (*result, error) {
	if err := report.CheckAuditFilter(&f); err != nil {
		return nil, usageError(err)
	}
	if err := s.need(s.audit, flagAudit, "tfcensus audit --format json"); err != nil {
		return nil, err
	}
	sv, err := s.read(s.audit, report.KindAudit)
	if err != nil {
		return nil, err
	}
	return &result{data: report.SummarizeAudit(sv.Audit, f), errs: sv.Errors}, nil
}

func (s *saved) driftSummary(f report.DriftFilter) (*result, error) {
	if err := report.CheckDriftFilter(&f); err != nil {
		return nil, usageError(err)
	}
	if err := s.need(s.drift, flagDrift, "tfcensus drift --format json"); err != nil {
		return nil, err
	}
	sv, err := s.read(s.drift, report.KindDrift)
	if err != nil {
		return nil, err
	}
	return &result{data: report.SummarizeDrift(sv.Drift, f), errs: sv.Errors}, nil
}

// sources lists the files a saved reader uses, for logs.
func (s *saved) sources() []string {
	var out []string
	for _, p := range []string{s.index, s.scan, s.audit, s.drift} {
		if p != "" {
			out = append(out, p)
		}
	}
	return slices.Clip(out)
}

// savedFiles completes the file names that report.Read and tfstate.ReadIndexFile accept.
func savedFiles() cobra.CompletionFunc { return complete.Files("json", "gz", "zst") }
