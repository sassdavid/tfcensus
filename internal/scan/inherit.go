package scan

import (
	"fmt"
	"slices"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// RuleInherit starts the rule of a finding that took its parent's category, as in
// inherit/aws-managed.
const RuleInherit = "inherit/"

// maxInheritDepth is the longest parent chain a category passes down.
const maxInheritDepth = 3

// Parent is the resource a finding took its category from, with that resource's own parent when
// it inherited too.
type Parent struct {
	CFNType  string   `json:"cfn_type"`
	Key      string   `json:"key"`
	Category Category `json:"category"`
	Owner    string   `json:"owner,omitempty"`
	Rule     string   `json:"rule"`
	Parent   *Parent  `json:"parent,omitempty"`
}

// Text describes the parent for reasons and explanations.
func (p *Parent) Text() string {
	return fmt.Sprintf("%s %s, %s (%s)", p.CFNType, p.Key, p.Category, p.Rule)
}

// childKind says which parent type a child type inherits from, and when.
type childKind struct {
	parent string
	when   func(*inventory.CloudResource) bool
}

// childKinds returns the child types that inherit their parent's category, by CloudFormation type.
func childKinds() map[string]childKind {
	always := func(*inventory.CloudResource) bool { return true }
	apexNSSOA := func(r *inventory.CloudResource) bool {
		return r.Attrs["apex"] == "true" && (r.Attrs["type"] == "NS" || r.Attrs["type"] == "SOA")
	}
	childConfig := func(r *inventory.CloudResource) bool { return r.Attrs["resource_configuration_type"] == "CHILD" }
	const (
		role, user, group, bucket = "AWS::IAM::Role", "AWS::IAM::User", "AWS::IAM::Group", "AWS::S3::Bucket"
		sg                        = "AWS::EC2::SecurityGroup"
	)
	return map[string]childKind{
		typemap.NativePrefix + "IAM::RolePolicyAttachment":   {role, always},
		typemap.NativePrefix + "IAM::UserPolicyAttachment":   {user, always},
		typemap.NativePrefix + "IAM::GroupPolicyAttachment":  {group, always},
		"AWS::IAM::RolePolicy":                               {role, always},
		"AWS::IAM::UserPolicy":                               {user, always},
		"AWS::IAM::GroupPolicy":                              {group, always},
		"AWS::S3::BucketPolicy":                              {bucket, always},
		typemap.NativePrefix + "S3::BucketVersioning":        {bucket, always},
		typemap.NativePrefix + "S3::BucketEncryption":        {bucket, always},
		typemap.NativePrefix + "S3::BucketLifecycle":         {bucket, always},
		typemap.NativePrefix + "S3::BucketPublicAccessBlock": {bucket, always},
		"AWS::EC2::SecurityGroupIngress":                     {sg, always},
		"AWS::EC2::SecurityGroupEgress":                      {sg, always},
		"AWS::Route53::RecordSet":                            {"AWS::Route53::HostedZone", apexNSSOA},
		"AWS::EC2::RouteTable":                               {"AWS::EC2::NatGateway", always},
		"AWS::VpcLattice::ResourceConfiguration":             {"AWS::VpcLattice::ResourceConfiguration", childConfig},
	}
}

// parentRef returns the parent r inherits from, when r is a child kind.
func parentRef(r *inventory.CloudResource) (inventory.Ref, bool) {
	k, ok := childKinds()[r.CFNType]
	if !ok || !k.when(r) {
		return inventory.Ref{}, false
	}
	for _, p := range r.Parents {
		if p.CFNType == k.parent {
			return p, true
		}
	}
	return inventory.Ref{}, false
}

// inheritable reports whether a parent in category c passes it to its children. A child of a
// managed parent stays unmanaged: something added to a managed resource is drift.
func inheritable(c Category) bool {
	return c == CategoryAWSManaged || c == CategoryOtherIaC || c == CategoryDerived
}

// inherit runs the second classification pass. An unmanaged or undetermined child takes the
// category of an inheritable parent. Each pass reads the categories of the pass before, so a
// chain of n parents takes n passes.
func inherit(out []Finding, results []match.Result, src Sources) {
	ix := newFindingIndex(results)
	parents := make([]int, len(out))
	for i := range results {
		parents[i] = -1
		if ref, ok := parentRef(&results[i].Resource); ok {
			parents[i] = ix.find(&results[i].Resource, ref)
		}
	}
	for range maxInheritDepth {
		prev := slices.Clone(out)
		changed := false
		for i := range out {
			p := parents[i]
			if p < 0 || p == i || (out[i].Category != CategoryUnmanaged && out[i].Category != CategoryUndetermined) {
				continue
			}
			pf := &prev[p]
			if !inheritable(pf.Category) || hidden(src, pf.Category, &results[i], &results[p]) {
				continue
			}
			parent := &Parent{
				CFNType:  pf.Type(),
				Key:      pf.Key(),
				Category: pf.Category,
				Owner:    pf.Owner,
				Rule:     pf.Rule,
				Parent:   pf.Parent,
			}
			out[i].Category, out[i].Owner, out[i].Rule = pf.Category, pf.Owner, RuleInherit+string(pf.Category)
			out[i].Reason, out[i].Parent = "child of "+parent.Text(), parent
			changed = true
		}
		if !changed {
			return
		}
	}
}

// hidden reports whether never_hide keeps a child from inheriting c: c is in never_hide and a
// guard matches the child or its parent.
func hidden(src Sources, c Category, child, parent *match.Result) bool {
	if src.Engine == nil || !src.Engine.NeverHides(ownership.Category(c)) {
		return false
	}
	for _, r := range []*match.Result{child, parent} {
		if src.Engine.Guarded(ownership.FromCloud(&r.Resource, src.Map, src.Index)) != "" {
			return true
		}
	}
	return false
}

// findingIndex finds a result by normalized ARN, or by account, region, type and ID.
type findingIndex struct {
	byARN map[string]int
	byID  map[string]int
}

func newFindingIndex(results []match.Result) *findingIndex {
	ix := &findingIndex{byARN: map[string]int{}, byID: map[string]int{}}
	for i := range results {
		r := &results[i].Resource
		if r.ARN != "" {
			ix.byARN[typemap.NormalizeARN(r.ARN)] = i
		}
		if r.ID != "" {
			ix.byID[r.Account+"|"+r.Region+"|"+r.CFNType+"|"+r.ID] = i
		}
	}
	return ix
}

// find returns the index of the result ref points at, looked up in r's account and region, then
// account-wide, or -1.
func (ix *findingIndex) find(r *inventory.CloudResource, ref inventory.Ref) int {
	if ref.ARN != "" {
		if i, ok := ix.byARN[typemap.NormalizeARN(ref.ARN)]; ok {
			return i
		}
	}
	if ref.ID == "" {
		return -1
	}
	for _, region := range []string{r.Region, ""} {
		if i, ok := ix.byID[r.Account+"|"+region+"|"+ref.CFNType+"|"+ref.ID]; ok {
			return i
		}
	}
	return -1
}
