package inventory

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	cfnEKSCluster  = "AWS::EKS::Cluster"
	cfnPodIdentity = "AWS::EKS::PodIdentityAssociation"
)

// eksPageSize is the most associations ListPodIdentityAssociations returns per page.
const eksPageSize = 100

// eksPodIdentity lists the Pod Identity associations of the clusters that earlier layers found,
// for the ownerArn that Resource Explorer leaves out: an add-on that owns an association.
type eksPodIdentity struct{ env *Env }

func (*eksPodIdentity) Name() string               { return LayerEKSPodIdentity }
func (*eksPodIdentity) Global() bool               { return false }
func (*eksPodIdentity) Supports(Capabilities) bool { return true }
func (*eksPodIdentity) Types() []string            { return []string{cfnPodIdentity} }
func (*eksPodIdentity) enriches()                  {}

func (l *eksPodIdentity) List(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return l.ListWith(ctx, acct, region, types, nil)
}

func (l *eksPodIdentity) ListWith(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
	known []CloudResource,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		clusters := clusterNames(known, acct.ID, region)
		if !wanted(types, cfnPodIdentity) || len(clusters) == 0 {
			return
		}
		client := eks.NewFromConfig(acct.Config, func(o *eks.Options) {
			o.Region = region
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceEKS))
		})
		for _, cluster := range clusters {
			if !l.cluster(ctx, client, acct, region, cluster, yield) {
				return
			}
		}
	}
}

// cluster yields the associations of one cluster and reports whether to go on. A cluster deleted
// since an earlier layer listed it has none.
func (*eksPodIdentity) cluster(
	ctx context.Context,
	client *eks.Client,
	acct *accounts.Account,
	region, name string,
	yield func(CloudResource, error) bool,
) bool {
	pages := eks.NewListPodIdentityAssociationsPaginator(client, &eks.ListPodIdentityAssociationsInput{
		ClusterName: aws.String(name), MaxResults: aws.Int32(eksPageSize),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if _, gone := errors.AsType[*ekstypes.ResourceNotFoundException](err); gone {
			return true
		}
		if err != nil {
			yield(CloudResource{}, fmt.Errorf("eks:ListPodIdentityAssociations: %w", err))
			return false
		}
		for i := range page.Associations {
			if !yield(podIdentity(acct, region, &page.Associations[i]), nil) {
				return false
			}
		}
	}
	return true
}

// clusterNames returns the names of the EKS clusters found in one account and region, sorted.
func clusterNames(known []CloudResource, account, region string) []string {
	var out []string
	for i := range known {
		r := &known[i]
		if r.CFNType != cfnEKSCluster || r.Account != account || r.Region != region {
			continue
		}
		name := r.ID
		if _, after, ok := strings.Cut(r.ARN, ":cluster/"); ok {
			name = after
		}
		if name != "" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func podIdentity(acct *accounts.Account, region string, a *ekstypes.PodIdentityAssociationSummary) CloudResource {
	id := aws.ToString(a.AssociationId)
	attrs := map[string]string{"cluster_name": aws.ToString(a.ClusterName), "association_id": id}
	if owner := aws.ToString(a.OwnerArn); owner != "" {
		attrs["owner_arn"] = owner
	}
	return CloudResource{
		Account: acct.ID, Region: region, CFNType: cfnPodIdentity, ID: id,
		ARN: typemap.NormalizeARN(aws.ToString(a.AssociationArn)), Attrs: attrs,
		Sources: []string{LayerEKSPodIdentity},
	}
}
