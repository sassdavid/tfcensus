package inventory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strconv"
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
			o.Retryer = accounts.AdaptiveRetryer(acct.Config)
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
			id := aws.ToString(d.Identifier)
			props, derr := l.properties(ctx, client, cfn, id, aws.ToString(d.Properties))
			if derr != nil {
				if !yield(CloudResource{}, derr) {
					return false
				}
				continue
			}
			if !yield(l.resource(acct, region, cfn, id, props, parent), nil) {
				return false
			}
		}
	}
	return true
}

// cfnResourceConfig is the VPC Lattice resource configuration type, whose type and group the
// layer reads so a CHILD configuration links to the configuration that created it.
const cfnResourceConfig = "AWS::VpcLattice::ResourceConfiguration"

// properties returns the listed properties, or reads them with GetResource for a resource
// configuration whose list entry leaves out its type.
func (l *cloudControl) properties(ctx context.Context, client *cloudcontrol.Client, cfn, id, listed string) (
	string, error,
) {
	if cfn != cfnResourceConfig || strings.Contains(listed, `"ResourceConfigurationType"`) {
		return listed, nil
	}
	return l.describe(ctx, client, cfn, id)
}

// describe reads one resource's properties with GetResource, for a list handler that returns
// identifiers only.
func (l *cloudControl) describe(ctx context.Context, client *cloudcontrol.Client, cfn, id string) (string, error) {
	out, err := client.GetResource(
		ctx,
		&cloudcontrol.GetResourceInput{TypeName: aws.String(cfn), Identifier: aws.String(id)},
	)
	if err != nil {
		return "", fmt.Errorf("cloudcontrol:GetResource %s: %w", cfn, err)
	}
	if out.ResourceDescription == nil {
		return "", nil
	}
	return aws.ToString(out.ResourceDescription.Properties), nil
}

// ccProps are the only properties the layer reads. The others, which can hold values a user
// wrote, are never decoded.
type ccProps struct {
	Arn                          string `json:"Arn"`
	ResourceConfigurationType    string `json:"ResourceConfigurationType"`
	ResourceConfigurationGroupID string `json:"ResourceConfigurationGroupId"`
	AmazonManaged                *bool  `json:"AmazonManaged"`
}

// resource builds a CloudResource from the identifier, the Arn property when the identifier
// isn't an ARN, and a resource configuration's type, group and AmazonManaged flag.
func (l *cloudControl) resource(acct *accounts.Account, region, cfn, id, props string, parent *Ref) CloudResource {
	r := CloudResource{Account: acct.ID, Region: region, CFNType: cfn, ID: id, Sources: []string{LayerCC}}
	var p ccProps
	_ = json.Unmarshal([]byte(props), &p) // a model that doesn't decode leaves only the identifier
	switch {
	case strings.HasPrefix(id, "arn:"):
		r.ARN = typemap.NormalizeARN(id)
	case strings.HasPrefix(p.Arn, "arn:"):
		r.ARN = typemap.NormalizeARN(p.Arn)
	}
	if cfn == cfnResourceConfig {
		l.resourceConfig(acct, region, &r, &p)
	}
	if parent != nil {
		r.Parents = []Ref{*parent}
		r.Relationships = []Ref{
			{CFNType: parent.CFNType, ARN: parent.ARN, ID: parent.ID, Relationship: "Is contained in"},
		}
	}
	return r
}

// resourceConfig keeps a resource configuration's type and AmazonManaged flag, and links a CHILD
// configuration to its group, the configuration AWS created it for.
func (*cloudControl) resourceConfig(acct *accounts.Account, region string, r *CloudResource, p *ccProps) {
	if p.ResourceConfigurationType != "" {
		r.setAttr("resource_configuration_type", p.ResourceConfigurationType)
	}
	if p.AmazonManaged != nil {
		r.setAttr("amazon_managed", strconv.FormatBool(*p.AmazonManaged))
	}
	group := p.ResourceConfigurationGroupID
	if p.ResourceConfigurationType != "CHILD" || group == "" {
		return
	}
	ref := Ref{CFNType: cfnResourceConfig, ID: group}
	if strings.HasPrefix(group, "arn:") {
		ref.ARN = group
		ref.ID = group[strings.LastIndex(group, "/")+1:]
	} else {
		ref.ARN = fmt.Sprintf(
			"arn:%s:vpc-lattice:%s:%s:resourceconfiguration/%s",
			acct.Partition,
			region,
			acct.ID,
			group,
		)
	}
	r.Parents = append(r.Parents, ref)
	ref.Relationship = "Is a child of"
	r.Relationships = append(r.Relationships, ref)
}
