package inventory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/configservice"
	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/accounts"
)

// UnmappedType is a resource type AWS Config records in one account and region that the typemap
// doesn't map, with the number of resources Config counted.
type UnmappedType struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	Type    string `json:"type"` // the AWS Config type
	Count   int64  `json:"count"`
}

// countsPageSize is the most resource counts GetDiscoveredResourceCounts returns per page.
const countsPageSize = 100

// countUnmapped asks AWS Config in every recording account and region how many resources of each
// type it records, and keeps the types the typemap doesn't map. A failed call becomes an error
// string; it doesn't fail the collection.
func (c *Collector) countUnmapped(ctx context.Context, targets []Target) ([]UnmappedType, []string) {
	type slot struct {
		acct   *accounts.Account
		region string
		counts map[string]int64
		err    error
	}
	var slots []*slot
	for i := range targets {
		for ri := range targets[i].Report.Regions {
			if CapabilitiesFrom(&targets[i].Report.Regions[ri]).Config {
				slots = append(slots, &slot{acct: targets[i].Account, region: targets[i].Report.Regions[ri].Region})
			}
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.opts.Concurrency)
	for _, s := range slots {
		g.Go(func() error {
			s.counts, s.err = c.resourceCounts(gctx, s.acct, s.region)
			return nil
		})
	}
	_ = g.Wait()
	var out []UnmappedType
	var errs []string
	for _, s := range slots {
		if s.err != nil {
			errs = append(errs, fmt.Sprintf("%s/%s: %v", s.acct.ID, s.region, s.err))
			c.opts.Env.logger().WarnContext(ctx, "could not count AWS Config resource types",
				"account", s.acct.ID, "region", s.region, "error", s.err)
			continue
		}
		for typ, n := range s.counts {
			if !c.opts.Env.Map.Mapped(c.opts.Env.Map.FromConfig(typ)) {
				out = append(out, UnmappedType{Account: s.acct.ID, Region: s.region, Type: typ, Count: n})
			}
		}
	}
	slices.SortFunc(out, func(a, b UnmappedType) int {
		return cmp.Or(strings.Compare(a.Account, b.Account), strings.Compare(a.Region, b.Region),
			cmp.Compare(b.Count, a.Count), strings.Compare(a.Type, b.Type))
	})
	return out, errs
}

func (c *Collector) resourceCounts(
	ctx context.Context,
	acct *accounts.Account,
	region string,
) (map[string]int64, error) {
	client := configservice.NewFromConfig(acct.Config, func(o *configservice.Options) {
		o.Region = region
		o.Retryer = accounts.AdaptiveRetryer(acct.Config)
		o.APIOptions = append(o.APIOptions, c.opts.Env.Limits.APIOption(acct.ID, region, ServiceConfig))
	})
	out := map[string]int64{}
	pages := configservice.NewGetDiscoveredResourceCountsPaginator(client,
		&configservice.GetDiscoveredResourceCountsInput{Limit: countsPageSize})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("config:GetDiscoveredResourceCounts: %w", err)
		}
		for _, rc := range page.ResourceCounts {
			out[string(rc.ResourceType)] += rc.Count
		}
	}
	return out, nil
}
