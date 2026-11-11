package inventory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/probe"
)

// Layer selections besides layer names.
const (
	LayersAuto   = "auto"   // every layer the capability probe allows
	LayersNative = "native" // every native layer
)

// Target is one account to inventory, with its probe result. The probe's regions are the
// regions listed.
type Target struct {
	Account *accounts.Account
	Report  *probe.Report
}

// Request says what to list.
type Request struct {
	Targets []Target
	// Layers names the layers to run: [LayersAuto] (or empty) for every layer the probe allows,
	// else layer names, where "native" stands for every native layer.
	Layers []string
	Types  []string // CloudFormation types; empty means every type
}

// RunStatus is the outcome of one layer in one account and region.
type RunStatus string

// Run outcomes.
const (
	RunOK          RunStatus = "ok"
	RunPartial     RunStatus = "partial" // some resources failed, such as one bucket
	RunDenied      RunStatus = "denied"
	RunError       RunStatus = "error"
	RunUnsupported RunStatus = "unsupported" // the probe says the source isn't set up
	RunSkipped     RunStatus = "skipped"     // none of its types are wanted, or earlier layers listed them
)

// Run is one layer's listing of one account and region. Global layers have an empty region.
type Run struct {
	Account string    `json:"account"`
	Region  string    `json:"region,omitempty"`
	Layer   string    `json:"layer"`
	Status  RunStatus `json:"status"`
	Count   int       `json:"count"`
	Errors  []string  `json:"errors,omitempty"`
}

// Result is the merged inventory.
type Result struct {
	Resources []CloudResource `json:"resources"`
	Runs      []Run           `json:"runs"`
	Coverage  *Coverage       `json:"coverage"`
}

// Options configures a [Collector].
type Options struct {
	Env         *Env
	Layers      []Layer // nil means [Layers]
	Concurrency int     // layer listings at the same time; 0 means 16 (4 accounts x 4 regions)
}

// Collector runs inventory layers and merges what they find.
type Collector struct {
	opts Options
}

const defaultConcurrency = 16

// NewCollector returns a Collector.
func NewCollector(opts Options) *Collector {
	if opts.Layers == nil {
		opts.Layers = Layers(opts.Env)
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	return &Collector{opts: opts}
}

// ErrUnknownLayer is returned for a layer name that no layer has.
var ErrUnknownLayer = errors.New("unknown layer")

type task struct {
	target *Target
	region string
	layer  Layer
	caps   Capabilities
	types  []string // the types to list; empty means every type of the layer
	run    Run
	found  []CloudResource
}

// Collect lists every target in two phases: every [Dependent] layer runs after the others and
// reads what they found. Each listing writes only its own slot, and the merge reads the slots in
// a fixed order, so the result doesn't depend on which listing finishes first. A failed listing
// is a [Run] with its error; Collect itself fails only when ctx is done.
func (c *Collector) Collect(ctx context.Context, req Request) (*Result, error) {
	layers, err := c.selected(req.Layers)
	if err != nil {
		return nil, err
	}
	auto := len(req.Layers) == 0 || slices.Equal(req.Layers, []string{LayersAuto})
	targets := slices.Clone(req.Targets)
	slices.SortFunc(targets, func(a, b Target) int { return strings.Compare(a.Account.ID, b.Account.ID) })
	tasks := plan(targets, layers)
	var first, second []*task
	for _, t := range tasks {
		switch {
		case auto && !t.layer.Supports(t.caps):
			t.run.Status = RunUnsupported
		case !slices.ContainsFunc(t.layer.Types(), func(c string) bool { return wanted(req.Types, c) }):
			t.run.Status = RunSkipped
		case isDependent(t.layer):
			second = append(second, t)
		default:
			t.types = req.Types
			first = append(first, t)
		}
	}
	if err = c.runAll(ctx, first, nil); err != nil {
		return nil, err
	}
	known := Merge(found(first), c.opts.Env)
	for _, t := range second {
		t.types = dependentTypes(t, first, req.Types, auto)
		if len(t.types) == 0 {
			t.run.Status = RunSkipped
		}
	}
	if err = c.runAll(ctx, second, known); err != nil {
		return nil, err
	}
	res := &Result{Runs: make([]Run, 0, len(tasks))}
	for _, t := range tasks {
		res.Runs = append(res.Runs, t.run)
	}
	res.Resources = Merge(found(tasks), c.opts.Env)
	res.Coverage = coverage(targets, tasks, layers, c.opts.Env, req.Types)
	return res, nil
}

func isDependent(l Layer) bool {
	_, ok := l.(Dependent)
	return ok
}

func found(tasks []*task) []CloudResource {
	var all []CloudResource
	for _, t := range tasks {
		all = append(all, t.found...)
	}
	return all
}

func (c *Collector) runAll(ctx context.Context, tasks []*task, known []CloudResource) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.opts.Concurrency)
	for _, t := range tasks {
		if t.run.Status != "" {
			continue
		}
		g.Go(func() error {
			c.list(gctx, t, known)
			return nil
		})
	}
	_ = g.Wait()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return nil
}

// dependentTypes returns the types a dependent layer lists in a task's account and region: its
// own types limited to want and, in auto mode, without the types an earlier layer listed there.
func dependentTypes(t *task, earlier []*task, want []string, auto bool) []string {
	var out []string
	for _, cfn := range t.layer.Types() {
		if !wanted(want, cfn) || (auto && coveredBy(earlier, t, cfn)) {
			continue
		}
		out = append(out, cfn)
	}
	return out
}

func coveredBy(earlier []*task, t *task, cfn string) bool {
	return slices.ContainsFunc(earlier, func(e *task) bool {
		return e.run.Account == t.run.Account && e.region == t.region &&
			(e.run.Status == RunOK || e.run.Status == RunPartial) && slices.Contains(e.layer.Types(), cfn)
	})
}

func (c *Collector) selected(names []string) ([]Layer, error) {
	if len(names) == 0 || slices.Equal(names, []string{LayersAuto}) {
		return c.opts.Layers, nil
	}
	var out []Layer
	for _, n := range names {
		n = cmp.Or(layerAliases()[n], n)
		found := false
		for _, l := range c.opts.Layers {
			if l.Name() == n || (n == LayersNative && strings.HasPrefix(l.Name(), LayersNative+":")) {
				found = true
				if !slices.Contains(out, l) {
					out = append(out, l)
				}
			}
		}
		if !found {
			return nil, fmt.Errorf(
				"%w %q: want auto, native, re, cc or one of %s",
				ErrUnknownLayer,
				n,
				strings.Join(LayerOrder(), ", "),
			)
		}
	}
	slices.SortFunc(out, func(a, b Layer) int { return cmp.Compare(layerRank(a.Name()), layerRank(b.Name())) })
	return out, nil
}

// plan lists the tasks in a fixed order: account, then region (global first), then layer.
func plan(targets []Target, layers []Layer) []*task {
	var tasks []*task
	for i := range targets {
		t := &targets[i]
		for _, l := range layers {
			if l.Global() && len(l.Types()) > 0 {
				tasks = append(tasks, &task{target: t, layer: l, run: Run{Account: t.Account.ID, Layer: l.Name()}})
			}
		}
		regions := slices.Clone(t.Report.Regions)
		slices.SortFunc(regions, func(a, b probe.Region) int { return strings.Compare(a.Region, b.Region) })
		for ri := range regions {
			caps := CapabilitiesFrom(&regions[ri])
			for _, l := range layers {
				if !l.Global() && len(l.Types()) > 0 {
					tasks = append(tasks, &task{
						target: t, region: regions[ri].Region, layer: l, caps: caps,
						run: Run{Account: t.Account.ID, Region: regions[ri].Region, Layer: l.Name()},
					})
				}
			}
		}
	}
	return tasks
}

func (c *Collector) list(ctx context.Context, t *task, known []CloudResource) {
	denied, failed := 0, 0
	listing := t.layer.List(ctx, t.target.Account, t.region, t.types)
	if d, ok := t.layer.(Dependent); ok {
		listing = d.ListWith(ctx, t.target.Account, t.region, t.types, known)
	}
	for r, err := range listing {
		if err != nil {
			t.run.Errors = append(t.run.Errors, err.Error())
			if Denied(err) {
				denied++
			} else {
				failed++
			}
			continue
		}
		t.found = append(t.found, r)
	}
	t.run.Count = len(t.found)
	switch {
	case denied+failed == 0:
		t.run.Status = RunOK
	case len(t.found) > 0:
		t.run.Status = RunPartial
	case failed == 0:
		t.run.Status = RunDenied
	default:
		t.run.Status = RunError
	}
	if t.run.Status != RunOK {
		c.opts.Env.logger().WarnContext(ctx, "inventory layer failed", "account", t.run.Account, "region", t.region,
			"layer", t.run.Layer, "status", string(t.run.Status), "errors", len(t.run.Errors))
	}
}

// mergeKey identifies a resource across layers: its ARN, or its type and ID without one.
func mergeKey(r *CloudResource) string {
	if r.ARN != "" {
		return r.ARN
	}
	return r.Account + "|" + r.Region + "|" + r.CFNType + "|" + r.ID
}

// Merge joins the records of the same resource. Records are folded in layer precedence order,
// so an earlier layer's non-empty value wins; a later layer fills only what is empty. Tags come
// from the first layer that has any. The result is sorted by account, region, type and key.
func Merge(records []CloudResource, env *Env) []CloudResource {
	sorted := slices.Clone(records)
	slices.SortStableFunc(sorted, func(a, b CloudResource) int {
		return cmp.Or(
			cmp.Compare(layerRank(firstSource(&a)), layerRank(firstSource(&b))),
			strings.Compare(mergeKey(&a), mergeKey(&b)),
		)
	})
	byKey := map[string]*CloudResource{}
	for i := range sorted {
		r := &sorted[i]
		key := mergeKey(r)
		cur, ok := byKey[key]
		if !ok {
			cp := *r
			byKey[key] = &cp
			continue
		}
		fold(cur, r, env)
	}
	out := make([]CloudResource, 0, len(byKey))
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		r := byKey[k]
		if env != nil && env.Map != nil && r.CFNType != "" {
			r.TFTypes = nil
			for _, t := range env.Map.ForCFN(r.CFNType) {
				r.TFTypes = append(r.TFTypes, t.TF)
			}
		}
		out = append(out, *r)
	}
	slices.SortStableFunc(out, func(a, b CloudResource) int {
		return cmp.Or(
			strings.Compare(a.Account, b.Account), strings.Compare(a.Region, b.Region),
			strings.Compare(a.CFNType, b.CFNType), strings.Compare(mergeKey(&a), mergeKey(&b)),
		)
	})
	return out
}

// appendMissing appends the elements of add that dst doesn't contain yet.
func appendMissing[T comparable](dst, add []T) []T {
	for _, v := range add {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}

func firstSource(r *CloudResource) string {
	if len(r.Sources) == 0 {
		return ""
	}
	return r.Sources[0]
}

func fold(cur, r *CloudResource, env *Env) {
	for _, f := range []struct {
		name     string
		dst, src *string
	}{
		{"account", &cur.Account, &r.Account},
		{"region", &cur.Region, &r.Region},
		{"cfn_type", &cur.CFNType, &r.CFNType},
		{"id", &cur.ID, &r.ID},
		{attrName, &cur.Name, &r.Name},
		{"created", &cur.Created, &r.Created},
	} {
		switch {
		case *f.dst == "":
			*f.dst = *f.src
		case *f.src != "" && *f.src != *f.dst && env != nil:
			env.logger().Debug("layers disagree; keeping the earlier layer's value", "resource", mergeKey(cur),
				"field", f.name, "kept", firstSource(cur), "dropped", firstSource(r))
		}
	}
	if cur.CFNType != "" {
		cur.SourceType = ""
	} else if cur.SourceType == "" {
		cur.SourceType = r.SourceType
	}
	if len(cur.Tags) == 0 && len(r.Tags) > 0 {
		cur.Tags = maps.Clone(r.Tags)
	}
	for k, v := range r.Attrs {
		if _, ok := cur.Attrs[k]; !ok {
			cur.setAttr(k, v)
		}
	}
	cur.Parents = appendMissing(cur.Parents, r.Parents)
	cur.Relationships = appendMissing(cur.Relationships, r.Relationships)
	cur.Sources = appendMissing(cur.Sources, r.Sources)
	slices.SortFunc(cur.Sources, func(a, b string) int { return cmp.Compare(layerRank(a), layerRank(b)) })
}
