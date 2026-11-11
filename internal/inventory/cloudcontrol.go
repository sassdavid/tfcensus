package inventory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// ccPageSize is the most resources one ListResources page asks for.
const ccPageSize = 100

// Dependent is a layer that runs after the other layers and reads what they found. Cloud Control
// lists some types only per parent, and takes the parents from the earlier layers.
type Dependent interface {
	ListWith(
		ctx context.Context,
		acct *accounts.Account,
		region string,
		types []string,
		known []CloudResource,
	) iter.Seq2[CloudResource, error]
}

type cloudControl struct{ env *Env }

// NewCloudControl returns the layer that lists the allowlisted types (Env.CloudControlTypes)
// with cloudformation:ListResources. A type with a cc_parent in the typemap is listed once per
// parent that an earlier layer, or Cloud Control itself, found in the same account and region.
func NewCloudControl(env *Env) Layer { return &cloudControl{env: env} }

func (*cloudControl) Name() string               { return LayerCC }
func (*cloudControl) Global() bool               { return false }
func (*cloudControl) Supports(Capabilities) bool { return true }

func (l *cloudControl) Types() []string {
	out := slices.Clone(l.env.CloudControlTypes)
	slices.Sort(out)
	return slices.Compact(out)
}

func (l *cloudControl) List(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return l.ListWith(ctx, acct, region, types, nil)
}

func (l *cloudControl) ListWith(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
	known []CloudResource,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		client := cloudcontrol.NewFromConfig(acct.Config, func(o *cloudcontrol.Options) {
			o.Region = region
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceCC))
		})
		found := slices.Clone(known)
		for _, cfn := range l.order(types) {
			var listed []CloudResource
			for r, err := range l.listType(ctx, client, acct, region, cfn, found) {
				if err == nil {
					listed = append(listed, r)
				}
				if !yield(r, err) {
					return
				}
			}
			found = append(found, listed...)
		}
	}
}

// order returns the wanted types with every parent type before its children, so children of a
// parent that only Cloud Control lists are found in the same run.
func (l *cloudControl) order(types []string) []string {
	all := l.Types()
	if len(types) > 0 {
		all = slices.DeleteFunc(all, func(t string) bool { return !slices.Contains(types, t) })
	}
	depth := func(cfn string) int {
		d := 0
		for p := l.env.Map.CCParent(cfn); p != nil && d < len(all); p = l.env.Map.CCParent(p.Type) {
			d++
		}
		return d
	}
	slices.SortStableFunc(all, func(a, b string) int { return cmp.Compare(depth(a), depth(b)) })
	return all
}

// ParentCovered reports whether a layer other than Cloud Control can list the parent type of a
// Cloud Control type that needs one, or Cloud Control lists it too. Types without a parent are
// always covered.
func ParentCovered(m *typemap.Map, cfn string, layers []Layer) bool {
	p := m.CCParent(cfn)
	if p == nil {
		return true
	}
	return slices.ContainsFunc(layers, func(l Layer) bool { return slices.Contains(l.Types(), p.Type) })
}

func (l *cloudControl) listType(
	ctx context.Context,
	client *cloudcontrol.Client,
	acct *accounts.Account,
	region, cfn string,
	known []CloudResource,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		parent := l.env.Map.CCParent(cfn)
		if parent == nil {
			l.page(ctx, client, acct, region, cfn, nil, nil, yield)
			return
		}
		for i := range known {
			p := &known[i]
			if p.CFNType != parent.Type || p.ARN == "" || p.Account != acct.ID || p.Region != region {
				continue
			}
			model, err := json.Marshal(map[string]string{parent.Property: p.ARN})
			if err != nil {
				yield(CloudResource{}, fmt.Errorf("encode resource model: %w", err))
				return
			}
			ref := &Ref{CFNType: p.CFNType, ARN: p.ARN, ID: p.ID}
			if !l.page(ctx, client, acct, region, cfn, aws.String(string(model)), ref, yield) {
				return
			}
		}
	}
}

// page yields every resource of one ListResources listing and reports whether to go on.
func (l *cloudControl) page(
	ctx context.Context,
	client *cloudcontrol.Client,
	acct *accounts.Account,
	region, cfn string,
	model *string,
	parent *Ref,
	yield func(CloudResource, error) bool,
) bool {
	pages := cloudcontrol.NewListResourcesPaginator(client, &cloudcontrol.ListResourcesInput{
		TypeName: aws.String(cfn), ResourceModel: model, MaxResults: aws.Int32(ccPageSize),
	})
	for pages.HasMorePages() {
		out, err := pages.NextPage(ctx)
		if err != nil {
			return yield(CloudResource{}, fmt.Errorf("cloudcontrol:ListResources %s: %w", cfn, err))
		}
		for _, d := range out.ResourceDescriptions {
			if !yield(
				l.resource(acct, region, cfn, aws.ToString(d.Identifier), aws.ToString(d.Properties), parent),
				nil,
			) {
				return false
			}
		}
	}
	return true
}

// resource builds a CloudResource from the identifier and, when the identifier isn't an ARN,
// the Arn property. No other property is read.
func (l *cloudControl) resource(acct *accounts.Account, region, cfn, id, props string, parent *Ref) CloudResource {
	r := CloudResource{Account: acct.ID, Region: region, CFNType: cfn, ID: id, Sources: []string{LayerCC}}
	if strings.HasPrefix(id, "arn:") {
		r.ARN = typemap.NormalizeARN(id)
	} else {
		var p struct {
			Arn string `json:"Arn"`
		}
		if json.Unmarshal([]byte(props), &p) == nil && strings.HasPrefix(p.Arn, "arn:") {
			r.ARN = typemap.NormalizeARN(p.Arn)
		}
	}
	if parent != nil {
		r.Parents = []Ref{*parent}
		r.Relationships = []Ref{
			{CFNType: parent.CFNType, ARN: parent.ARN, ID: parent.ID, Relationship: "Is contained in"},
		}
	}
	return r
}
