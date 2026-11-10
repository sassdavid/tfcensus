package probe

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	cstypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	retypes "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/accounts"
)

// DefaultTTL is how long a probe result stays fresh.
const DefaultTTL = 24 * time.Hour

// Status is the outcome of one check.
type Status string

// Check outcomes.
const (
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable" // the feature is off or not set up
	StatusDenied      Status = "denied"      // the credentials may not call the API
	StatusError       Status = "error"       // the API failed otherwise
)

// Layer is an inventory source.
type Layer string

// Inventory layers, in the order the scan prefers them.
const (
	LayerRE     Layer = "re"
	LayerConfig Layer = "config"
	LayerCC     Layer = "cc"
	LayerNative Layer = "native"
)

// Index types.
const (
	IndexLocal      = "local"
	IndexAggregator = "aggregator"
)

// Report is the probe result for one account.
type Report struct {
	Account          string     `json:"account"`
	Name             string     `json:"name,omitempty"`
	ResourceExplorer ResourceEx `json:"resource_explorer"`
	Regions          []Region   `json:"regions"`
	Notes            []string   `json:"notes"`
}

// ResourceEx is the account-wide Resource Explorer state: one ListIndexes call covers every region.
type ResourceEx struct {
	Status         Status    `json:"status"`
	Indexes        []Index   `json:"indexes"`
	SupportedTypes int       `json:"supported_types,omitempty"`
	Error          string    `json:"error,omitempty"`
	ProbedAt       time.Time `json:"probed_at"`
	Cached         bool      `json:"cached"`
}

// Index is a Resource Explorer index.
type Index struct {
	Region string `json:"region"`
	Type   string `json:"type"` // local or aggregator
}

// Region is the probe result for one region of an account.
type Region struct {
	Region           string      `json:"region"`
	ResourceExplorer RegionRE    `json:"resource_explorer"`
	Config           ConfigCheck `json:"config"`
	Aggregators      AggCheck    `json:"aggregators"`
	CloudControl     Status      `json:"cloud_control"`
	Layers           []Layer     `json:"layers"`
	ProbedAt         time.Time   `json:"probed_at"`
	Cached           bool        `json:"cached"`
}

// RegionRE is Resource Explorer in one region. ListResources needs a view, and tfcensus never
// creates one.
type RegionRE struct {
	Status      Status `json:"status"`
	Index       string `json:"index,omitempty"` // local, aggregator, or empty for none
	Views       int    `json:"views"`
	DefaultView bool   `json:"default_view"`
	Error       string `json:"error,omitempty"`
}

// ConfigCheck is the AWS Config recorder state in one region.
type ConfigCheck struct {
	Status    Status     `json:"status"`
	Recorders []Recorder `json:"recorders"`
	Error     string     `json:"error,omitempty"`
}

// Recorder is one AWS Config configuration recorder.
type Recorder struct {
	Name          string `json:"name"`
	Recording     bool   `json:"recording"`
	AllSupported  bool   `json:"all_supported"`
	IncludeGlobal bool   `json:"include_global"`
	Frequency     string `json:"frequency"` // CONTINUOUS or DAILY
}

// AggCheck lists the AWS Config aggregators in one region of the account.
type AggCheck struct {
	Status Status   `json:"status"`
	Names  []string `json:"names"`
	Error  string   `json:"error,omitempty"`
}

// Options configures a [Prober].
type Options struct {
	CacheDir    string // probe results are kept here; empty turns the cache off
	TTL         time.Duration
	Refresh     bool // probe again even when the cache is fresh
	Clock       func() time.Time
	Logger      *slog.Logger
	Concurrency int // regions probed in parallel per account
}

// Prober checks which inventory layers each account and region supports.
type Prober struct {
	opts Options
}

const defaultConcurrency = 4

// New returns a Prober. Zero Options fields get defaults.
func New(opts Options) *Prober {
	if opts.TTL == 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	return &Prober{opts: opts}
}

// Probe checks acct in each region. Without regions it probes the regions that have a Resource
// Explorer index and the account's default region. It fails only when ctx is done; failed checks
// are part of the report.
func (p *Prober) Probe(ctx context.Context, acct *accounts.Account, regions []string) (*Report, error) {
	r := &Report{Account: acct.ID, Name: acct.Name}
	if !p.load(acct.ID, "account", &r.ResourceExplorer) {
		r.ResourceExplorer = p.probeAccount(ctx, acct.Config)
		if r.ResourceExplorer.Status != StatusError && r.ResourceExplorer.Error == "" {
			p.save(ctx, acct.ID, "account", r.ResourceExplorer)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if len(regions) == 0 {
		regions = defaultRegions(r.ResourceExplorer.Indexes, acct.Config.Region)
	}
	regions = sortedUnique(regions)
	r.Regions = make([]Region, len(regions))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.opts.Concurrency)
	for i, region := range regions {
		index := indexIn(r.ResourceExplorer.Indexes, region)
		g.Go(func() error {
			reg := &r.Regions[i]
			if !p.load(acct.ID, region, reg) || reg.ResourceExplorer.Index != index {
				*reg = p.probeRegion(gctx, acct.Config, region, index)
				if !failed(reg) {
					p.save(gctx, acct.ID, region, *reg)
				}
			}
			return gctx.Err()
		})
	}
	if err := g.Wait(); err != nil {
		return nil, context.Cause(ctx)
	}
	for i := range r.Regions {
		r.Regions[i].Layers = layers(&r.Regions[i])
	}
	r.Notes = notes(r)
	return r, nil
}

// failed reports whether a check failed for a reason other than access, which may be gone on the
// next run, so the result isn't cached.
func failed(r *Region) bool {
	return r.ResourceExplorer.Status == StatusError || r.Config.Status == StatusError ||
		r.Aggregators.Status == StatusError
}

func defaultRegions(indexes []Index, home string) []string {
	out := []string{home}
	for _, ix := range indexes {
		out = append(out, ix.Region)
	}
	return out
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(slices.DeleteFunc(out, func(s string) bool { return s == "" }))
}

func indexIn(indexes []Index, region string) string {
	for _, ix := range indexes {
		if ix.Region == region {
			return ix.Type
		}
	}
	return ""
}

func (p *Prober) probeAccount(ctx context.Context, cfg aws.Config) ResourceEx {
	re := ResourceEx{ProbedAt: p.now(), Indexes: []Index{}}
	client := resourceexplorer2.NewFromConfig(cfg)
	pages := resourceexplorer2.NewListIndexesPaginator(client, &resourceexplorer2.ListIndexesInput{})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			re.Status, re.Error = failure("resource-explorer-2:ListIndexes", err)
			return re
		}
		for _, ix := range page.Indexes {
			re.Indexes = append(re.Indexes, Index{Region: aws.ToString(ix.Region), Type: indexType(ix.Type)})
		}
	}
	slices.SortFunc(re.Indexes, func(a, b Index) int { return strings.Compare(a.Region, b.Region) })
	if len(re.Indexes) == 0 {
		re.Status = StatusUnavailable
		return re
	}
	re.Status = StatusAvailable
	// The supported types are the same in every region; ask one that has an index.
	typesCfg := cfg.Copy()
	typesCfg.Region = re.Indexes[0].Region
	for _, ix := range re.Indexes {
		if ix.Type == IndexAggregator {
			typesCfg.Region = ix.Region
		}
	}
	tp := resourceexplorer2.NewListSupportedResourceTypesPaginator(
		resourceexplorer2.NewFromConfig(typesCfg), &resourceexplorer2.ListSupportedResourceTypesInput{})
	for tp.HasMorePages() {
		page, err := tp.NextPage(ctx)
		if err != nil {
			_, re.Error = failure("resource-explorer-2:ListSupportedResourceTypes", err)
			re.SupportedTypes = 0
			return re
		}
		re.SupportedTypes += len(page.ResourceTypes)
	}
	return re
}

func indexType(t retypes.IndexType) string {
	if t == retypes.IndexTypeAggregator {
		return IndexAggregator
	}
	return IndexLocal
}

func (p *Prober) probeRegion(ctx context.Context, base aws.Config, region, index string) Region {
	cfg := base.Copy()
	cfg.Region = region
	return Region{
		Region:           region,
		ResourceExplorer: probeViews(ctx, cfg, index),
		Config:           probeRecorders(ctx, cfg),
		Aggregators:      probeAggregators(ctx, cfg),
		CloudControl:     StatusAvailable,
		ProbedAt:         p.now(),
	}
}

func probeViews(ctx context.Context, cfg aws.Config, index string) RegionRE {
	re := RegionRE{Index: index, Status: StatusUnavailable}
	if index == "" {
		return re
	}
	client := resourceexplorer2.NewFromConfig(cfg)
	pages := resourceexplorer2.NewListViewsPaginator(client, &resourceexplorer2.ListViewsInput{})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			re.Status, re.Error = failure("resource-explorer-2:ListViews", err)
			return re
		}
		re.Views += len(page.Views)
	}
	dv, err := client.GetDefaultView(ctx, &resourceexplorer2.GetDefaultViewInput{})
	if err != nil {
		re.Status, re.Error = failure("resource-explorer-2:GetDefaultView", err)
		return re
	}
	re.DefaultView = aws.ToString(dv.ViewArn) != ""
	if re.Views > 0 || re.DefaultView {
		re.Status = StatusAvailable
	}
	return re
}

func probeRecorders(ctx context.Context, cfg aws.Config) ConfigCheck {
	c := ConfigCheck{Status: StatusUnavailable, Recorders: []Recorder{}}
	client := configservice.NewFromConfig(cfg)
	recs, err := client.DescribeConfigurationRecorders(ctx, &configservice.DescribeConfigurationRecordersInput{})
	if err != nil {
		c.Status, c.Error = failure("config:DescribeConfigurationRecorders", err)
		return c
	}
	if len(recs.ConfigurationRecorders) == 0 {
		return c
	}
	status, err := client.DescribeConfigurationRecorderStatus(ctx,
		&configservice.DescribeConfigurationRecorderStatusInput{})
	if err != nil {
		c.Status, c.Error = failure("config:DescribeConfigurationRecorderStatus", err)
		return c
	}
	recording := map[string]bool{}
	for _, s := range status.ConfigurationRecordersStatus {
		recording[aws.ToString(s.Name)] = s.Recording
	}
	for _, r := range recs.ConfigurationRecorders {
		rec := Recorder{Name: aws.ToString(r.Name), Recording: recording[aws.ToString(r.Name)], Frequency: "CONTINUOUS"}
		if g := r.RecordingGroup; g != nil {
			rec.AllSupported, rec.IncludeGlobal = g.AllSupported, g.IncludeGlobalResourceTypes
		}
		if m := r.RecordingMode; m != nil && m.RecordingFrequency != "" {
			rec.Frequency = string(m.RecordingFrequency)
		}
		if rec.Recording {
			c.Status = StatusAvailable
		}
		c.Recorders = append(c.Recorders, rec)
	}
	slices.SortFunc(c.Recorders, func(a, b Recorder) int { return strings.Compare(a.Name, b.Name) })
	return c
}

func probeAggregators(ctx context.Context, cfg aws.Config) AggCheck {
	a := AggCheck{Status: StatusUnavailable, Names: []string{}}
	client := configservice.NewFromConfig(cfg)
	in := &configservice.DescribeConfigurationAggregatorsInput{}
	for {
		out, err := client.DescribeConfigurationAggregators(ctx, in)
		if err != nil {
			a.Status, a.Error = failure("config:DescribeConfigurationAggregators", err)
			return a
		}
		for _, agg := range out.ConfigurationAggregators {
			a.Names = append(a.Names, aws.ToString(agg.ConfigurationAggregatorName))
		}
		if aws.ToString(out.NextToken) == "" {
			break
		}
		in.NextToken = out.NextToken
	}
	if len(a.Names) > 0 {
		a.Status = StatusAvailable
	}
	slices.Sort(a.Names)
	return a
}

// Aggregator checks that the named AWS Config aggregator exists, for org-wide queries.
func Aggregator(ctx context.Context, cfg aws.Config, region, name string) AggCheck {
	c := cfg.Copy()
	c.Region = cmp.Or(region, c.Region)
	a := AggCheck{Status: StatusUnavailable, Names: []string{}}
	out, err := configservice.NewFromConfig(c).DescribeConfigurationAggregators(ctx,
		&configservice.DescribeConfigurationAggregatorsInput{ConfigurationAggregatorNames: []string{name}})
	_, missing := errors.AsType[*cstypes.NoSuchConfigurationAggregatorException](err)
	switch {
	case missing:
		return a
	case err != nil:
		a.Status, a.Error = failure("config:DescribeConfigurationAggregators", err)
		return a
	}
	for _, agg := range out.ConfigurationAggregators {
		a.Names = append(a.Names, aws.ToString(agg.ConfigurationAggregatorName))
		a.Status = StatusAvailable
	}
	return a
}

func failure(action string, err error) (Status, string) {
	if accounts.IsAccessDenied(err) {
		return StatusDenied, "access denied for " + action
	}
	return StatusError, action + ": " + err.Error()
}

func layers(r *Region) []Layer {
	var out []Layer
	if r.ResourceExplorer.Status == StatusAvailable {
		out = append(out, LayerRE)
	}
	if r.Config.Status == StatusAvailable {
		out = append(out, LayerConfig)
	}
	return append(out, LayerCC, LayerNative)
}

func (p *Prober) now() time.Time { return p.opts.Clock().UTC().Truncate(time.Second) }

type cached interface {
	*ResourceEx | *Region
}

func (p *Prober) path(account, name string) string {
	return filepath.Join(p.opts.CacheDir, "probe", account, name+".json")
}

// load reads a fresh cached result into v and reports whether it did.
func load[T cached](p *Prober, account, name string, v T) bool {
	if p.opts.CacheDir == "" || p.opts.Refresh {
		return false
	}
	b, err := os.ReadFile(p.path(account, name))
	if err != nil || json.Unmarshal(b, v) != nil {
		return false
	}
	var at time.Time
	switch x := any(v).(type) {
	case *ResourceEx:
		at, x.Cached = x.ProbedAt, true
	case *Region:
		at, x.Cached = x.ProbedAt, true
	}
	return p.opts.Clock().Sub(at) < p.opts.TTL
}

func (p *Prober) load(account, name string, v any) bool {
	switch x := v.(type) {
	case *ResourceEx:
		return load(p, account, name, x)
	case *Region:
		return load(p, account, name, x)
	}
	return false
}

func (p *Prober) save(ctx context.Context, account, name string, v any) {
	if p.opts.CacheDir == "" || ctx.Err() != nil {
		return
	}
	path := p.path(account, name)
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err == nil {
		var b []byte
		if b, err = json.Marshal(v); err == nil {
			err = os.WriteFile(path, b, 0o600)
		}
	}
	if err != nil {
		p.opts.Logger.WarnContext(ctx, "cannot cache probe result", "path", path, "error", err)
	}
}

func notes(r *Report) []string {
	out := []string{}
	re := r.ResourceExplorer
	switch re.Status {
	case StatusUnavailable:
		out = append(out, "Resource Explorer is off. An admin can create an index in each region "+
			"(`aws resource-explorer-2 create-index`) to turn on the re layer.")
	case StatusAvailable:
		if re.SupportedTypes > 0 {
			out = append(out, fmt.Sprintf("Resource Explorer supports %d resource types.", re.SupportedTypes))
		}
		if !slices.ContainsFunc(re.Indexes, func(ix Index) bool {
			return ix.Type == IndexAggregator
		}) {
			out = append(out, "Resource Explorer has local indexes only, so tfcensus searches each region on its own. "+
				"An admin can promote one to an aggregator (`aws resource-explorer-2 update-index-type --type AGGREGATOR`).")
		}
	case StatusDenied, StatusError:
	}
	if re.Error != "" {
		out = append(out, errorNote(re.Error))
	}
	for i := range r.Regions {
		out = append(out, regionNotes(&r.Regions[i], re.Status)...)
	}
	return out
}

func regionNotes(reg *Region, accountRE Status) []string {
	var out []string
	add := func(s string) { out = append(out, reg.Region+": "+s) }
	rre := reg.ResourceExplorer
	switch {
	case rre.Error != "":
		add(errorNote(rre.Error))
	case rre.Index == "" && accountRE == StatusAvailable:
		add(fmt.Sprintf("no Resource Explorer index. An admin can create one "+
			"(`aws resource-explorer-2 create-index --region %s`).", reg.Region))
	case rre.Index != "" && rre.Status == StatusUnavailable:
		add("the Resource Explorer index has no view, and tfcensus never creates one. An admin can create a " +
			"default view (`aws resource-explorer-2 create-view`, then `associate-default-view`).")
	}
	switch {
	case reg.Config.Error != "":
		add(errorNote(reg.Config.Error))
	case len(reg.Config.Recorders) == 0:
		add("no AWS Config recorder, so the config layer is off here.")
	case reg.Config.Status == StatusUnavailable:
		for _, rec := range reg.Config.Recorders {
			add(fmt.Sprintf("AWS Config recorder %s is stopped, so the config layer is off here.", rec.Name))
		}
	}
	if reg.Aggregators.Error != "" {
		add(errorNote(reg.Aggregators.Error))
	}
	return out
}

func errorNote(msg string) string {
	if strings.HasPrefix(msg, "access denied") {
		return msg + ". Grant the actions in docs/iam/tfcensus-readonly.json."
	}
	return msg
}
