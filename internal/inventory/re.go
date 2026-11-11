package inventory

import (
	"cmp"
	"context"
	"fmt"
	"iter"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	retypes "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// rePageSize stays under the 1,000 results ListResources allows per page.
const rePageSize = 500

type reLayer struct{ env *Env }

// NewResourceExplorer returns the layer that lists a region through its Resource Explorer default
// view. It never creates a view, and it never picks one of several views: without a default view
// the layer doesn't run.
func NewResourceExplorer(env *Env) Layer { return &reLayer{env: env} }

func (l *reLayer) Name() string                 { return LayerRE }
func (l *reLayer) Global() bool                 { return false }
func (l *reLayer) Supports(c Capabilities) bool { return c.ResourceExplorer }

func (l *reLayer) Types() []string {
	return curatedTypes(l.env.Map, func(t *typemap.Type) bool { return len(t.RE) > 0 })
}

// reFilter limits a listing to one region. An aggregator index returns every region otherwise.
// Global resources are indexed in the partition's global region.
func reFilter(acct *accounts.Account, region string) string {
	f := "region:" + region
	if region == GlobalRegion(acct.Partition) {
		f += " region:global"
	}
	return f
}

func (l *reLayer) List(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		client := resourceexplorer2.NewFromConfig(acct.Config, func(o *resourceexplorer2.Options) {
			o.Region = region
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceRE))
		})
		pages := resourceexplorer2.NewListResourcesPaginator(client, &resourceexplorer2.ListResourcesInput{
			Filters:    &retypes.SearchFilter{FilterString: aws.String(reFilter(acct, region))},
			MaxResults: aws.Int32(rePageSize),
		})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(CloudResource{}, fmt.Errorf("resource-explorer-2:ListResources: %w", err))
				return
			}
			for i := range page.Resources {
				r := l.resource(&page.Resources[i], acct)
				if r.CFNType != "" && !wanted(types, r.CFNType) {
					continue
				}
				if !yield(r, nil) {
					return
				}
			}
		}
	}
}

func (l *reLayer) resource(res *retypes.Resource, acct *accounts.Account) CloudResource {
	arn := typemap.NormalizeARN(aws.ToString(res.Arn))
	reType := aws.ToString(res.ResourceType)
	classified, _ := l.env.Map.Classify(arn)
	r := CloudResource{
		Account: cmp.Or(aws.ToString(res.OwningAccountId), acct.ID),
		Region:  aws.ToString(res.Region),
		CFNType: cmp.Or(classified, aws.ToString(res.CfnResourceType), l.env.Map.FromRE(reType)),
		ARN:     arn,
		Sources: []string{LayerRE},
	}
	if r.Region == "global" {
		r.Region = ""
	}
	if r.CFNType == "" {
		r.SourceType = reType
	}
	for _, p := range res.Properties {
		if aws.ToString(p.Name) != "tags" || p.Data == nil {
			continue
		}
		var tags []struct{ Key, Value string }
		if err := p.Data.UnmarshalSmithyDocument(&tags); err != nil {
			continue
		}
		for _, t := range tags {
			if r.Tags == nil {
				r.Tags = map[string]string{}
			}
			r.Tags[t.Key] = t.Value
		}
	}
	return r
}
