package audit

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3control"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/iampolicy"
)

const s3ObjectType = "AWS::S3::Object"

// accountPublicAccess reads the account's public access block once per account.
func (a *auditor) accountPublicAccess(ctx context.Context, acct *accounts.Account) pabResult {
	return cached(a, a.accountPAB, acct.ID, func() pabResult {
		// The block is account-wide. The account's own region, not the region of whichever bucket
		// asks first, keeps the request the same on every run.
		region := cmp.Or(acct.Config.Region, "us-east-1")
		c := s3control.NewFromConfig(acct.Config, func(o *s3control.Options) { o.Region = region })
		out, err := c.GetPublicAccessBlock(ctx, &s3control.GetPublicAccessBlockInput{AccountId: aws.String(acct.ID)})
		if apiCode(err) == "NoSuchPublicAccessBlockConfiguration" {
			return pabResult{}
		}
		if err != nil {
			return pabResult{err: fmt.Errorf("get account public access block: %w", err)}
		}
		c2 := out.PublicAccessBlockConfiguration
		if c2 == nil {
			return pabResult{}
		}
		return pabResult{settings: [4]bool{
			aws.ToBool(c2.BlockPublicAcls), aws.ToBool(c2.IgnorePublicAcls),
			aws.ToBool(c2.BlockPublicPolicy), aws.ToBool(c2.RestrictPublicBuckets),
		}}
	})
}

// trailResult is what the trails visible in one account and region log as S3 data events.
type trailResult struct {
	all      bool     // some trail logs data events of every bucket
	prefixes []string // ARN prefixes such as arn:aws:s3:::bucket/
	err      error
}

func (t *trailResult) covers(bucket string) bool {
	if t.all {
		return true
	}
	object := "arn:aws:s3:::" + bucket + "/terraform.tfstate"
	return slices.ContainsFunc(t.prefixes, func(p string) bool { return strings.HasPrefix(object, p) })
}

// dataEventTrails reads the event selectors of every trail visible in the region, organization
// trails included. A shadow trail whose selectors can't be read is skipped.
func (a *auditor) dataEventTrails(ctx context.Context, acct *accounts.Account, region string) trailResult {
	return cached(a, a.trails, acct.ID+"\x00"+region, func() trailResult {
		c := cloudtrail.NewFromConfig(acct.Config, func(o *cloudtrail.Options) { o.Region = region })
		out, err := c.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{IncludeShadowTrails: aws.Bool(true)})
		if err != nil {
			return trailResult{err: fmt.Errorf("describe trails: %w", err)}
		}
		var res trailResult
		for _, tr := range out.TrailList {
			sel, selErr := c.GetEventSelectors(ctx, &cloudtrail.GetEventSelectorsInput{TrailName: tr.TrailARN})
			if selErr != nil {
				a.opts.Logger.DebugContext(ctx, "skipping trail", "trail", aws.ToString(tr.TrailARN), "error", selErr)
				continue
			}
			res.addBasic(sel.EventSelectors)
			res.addAdvanced(sel.AdvancedEventSelectors)
		}
		return res
	})
}

func (t *trailResult) addBasic(selectors []cttypes.EventSelector) {
	for _, s := range selectors {
		for _, dr := range s.DataResources {
			if aws.ToString(dr.Type) != s3ObjectType {
				continue
			}
			for _, v := range dr.Values {
				if v == "arn:aws:s3" || v == "arn:aws:s3:::" {
					t.all = true
				} else {
					t.prefixes = append(t.prefixes, v)
				}
			}
		}
	}
}

// addAdvanced reads advanced selectors that select data events of S3 objects. Only Equals and
// StartsWith on resources.ARN narrow them; other ARN operators count as not covering.
func (t *trailResult) addAdvanced(selectors []cttypes.AdvancedEventSelector) {
	for _, s := range selectors {
		data, objects, narrowed := false, false, false
		var prefixes []string
		for _, f := range s.FieldSelectors {
			switch aws.ToString(f.Field) {
			case "eventCategory":
				data = slices.Contains(f.Equals, "Data")
			case "resources.type":
				objects = slices.Contains(f.Equals, s3ObjectType)
			case "resources.ARN":
				narrowed = true
				prefixes = append(prefixes, f.StartsWith...)
				prefixes = append(prefixes, f.Equals...)
			}
		}
		if !data || !objects {
			continue
		}
		if !narrowed {
			t.all = true
			continue
		}
		t.prefixes = append(t.prefixes, prefixes...)
	}
}

// discover lists the buckets of every resolved account and of the accounts that hold unit
// backends, and returns those matching the patterns that no unit names.
func (a *auditor) discover(ctx context.Context, known []*bucketTarget, rep *Report) []*bucketTarget {
	seen := map[string]bool{}
	for _, t := range known {
		seen[t.name] = true
	}
	var out []*bucketTarget
	for _, acct := range a.discoverAccounts(ctx, known) {
		out = append(out, a.discoverIn(ctx, acct, seen, rep)...)
	}
	slices.SortFunc(out, func(x, y *bucketTarget) int { return strings.Compare(x.name, y.name) })
	return out
}

// discoverAccounts returns the resolved accounts and the accounts of the unit backends, one per ID.
func (a *auditor) discoverAccounts(ctx context.Context, known []*bucketTarget) []*accounts.Account {
	var out []*accounts.Account
	ids := map[string]bool{}
	add := func(acct *accounts.Account) {
		if acct != nil && !ids[acct.ID] {
			ids[acct.ID] = true
			out = append(out, acct)
		}
	}
	for _, acct := range a.opts.Accounts {
		add(acct)
	}
	for _, t := range known {
		if acct, _, err := a.credentials(ctx, t); err == nil {
			add(acct)
		}
	}
	return out
}

// discoverIn lists the buckets of one account and returns the unseen ones matching a pattern.
func (a *auditor) discoverIn(
	ctx context.Context, acct *accounts.Account, seen map[string]bool, rep *Report,
) []*bucketTarget {
	region := cmp.Or(acct.Config.Region, "us-east-1")
	client := a.opts.S3(acct, region)
	var out []*bucketTarget
	p := s3.NewListBucketsPaginator(client, &s3.ListBucketsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			rep.Incomplete = append(
				rep.Incomplete,
				problem("account "+acct.ID, "", fmt.Errorf("list buckets: %w", err)),
			)
			return out
		}
		for _, b := range page.Buckets {
			name := aws.ToString(b.Name)
			if seen[name] || !matchesAny(a.opts.Discover, name) {
				continue
			}
			seen[name] = true
			t := &bucketTarget{
				name: name, acct: acct, region: aws.ToString(b.BucketRegion), source: SourceDiscover,
				known: map[string]bool{},
			}
			if t.region == "" {
				if t.region, err = bucketRegion(ctx, client, name); err != nil {
					rep.Incomplete = append(rep.Incomplete, problem(t.target(), "", err))
					continue
				}
			}
			out = append(out, t)
		}
	}
	return out
}

func matchesAny(patterns []string, name string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return iampolicy.Glob(p, name, false) })
}
