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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// attrName is the name attribute of inline policies and records, as the provider calls it.
const attrName = "name"

// Native type names that CloudFormation lacks, shared with data/curated.yaml.
const (
	cfnSGIngress       = "AWS::EC2::SecurityGroupIngress"
	cfnSGEgress        = "AWS::EC2::SecurityGroupEgress"
	cfnSecurityGroup   = "AWS::EC2::SecurityGroup"
	cfnRouteTable      = "AWS::EC2::RouteTable"
	cfnNatGateway      = "AWS::EC2::NatGateway"
	cfnRoleAttachment  = typemap.NativePrefix + "IAM::RolePolicyAttachment"
	cfnUserAttachment  = typemap.NativePrefix + "IAM::UserPolicyAttachment"
	cfnGroupAttachment = typemap.NativePrefix + "IAM::GroupPolicyAttachment"
	cfnRolePolicy      = "AWS::IAM::RolePolicy"
	cfnUserPolicy      = "AWS::IAM::UserPolicy"
	cfnGroupPolicy     = "AWS::IAM::GroupPolicy"
	cfnRole            = "AWS::IAM::Role"
	cfnUser            = "AWS::IAM::User"
	cfnGroup           = "AWS::IAM::Group"
	cfnManagedPolicy   = "AWS::IAM::ManagedPolicy"
	cfnHostedZone      = "AWS::Route53::HostedZone"
	cfnRecordSet       = "AWS::Route53::RecordSet"
	cfnBucket          = "AWS::S3::Bucket"
	cfnBucketPolicy    = "AWS::S3::BucketPolicy"
	cfnBucketVersions  = typemap.NativePrefix + "S3::BucketVersioning"
	cfnBucketEncrypt   = typemap.NativePrefix + "S3::BucketEncryption"
	cfnBucketLifecycle = typemap.NativePrefix + "S3::BucketLifecycle"
	cfnBucketPAB       = typemap.NativePrefix + "S3::BucketPublicAccessBlock"
)

// NativeLayers returns the hand-written listers for types that the other layers miss or report
// as attributes of a parent.
func NativeLayers(env *Env) []Layer {
	return []Layer{&sgRules{env}, &natGateways{env}, &iamAttachments{env}, &route53Records{env}, &s3Subresources{env}}
}

// Layers returns every layer in precedence order.
func Layers(env *Env) []Layer {
	return append(append([]Layer{NewConfig(env), NewResourceExplorer(env)}, NativeLayers(env)...), NewCloudControl(env))
}

type sgRules struct{ env *Env }

func (*sgRules) Name() string               { return LayerSGRules }
func (*sgRules) Global() bool               { return false }
func (*sgRules) Supports(Capabilities) bool { return true }
func (*sgRules) Types() []string            { return []string{cfnSGEgress, cfnSGIngress} }

// sgRulePageSize is the most rules DescribeSecurityGroupRules returns per page.
const sgRulePageSize = 1000

func (l *sgRules) List(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		if !wanted(types, cfnSGIngress) && !wanted(types, cfnSGEgress) {
			return
		}
		client := ec2.NewFromConfig(acct.Config, func(o *ec2.Options) {
			o.Region = region
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceEC2))
		})
		pages := ec2.NewDescribeSecurityGroupRulesPaginator(client,
			&ec2.DescribeSecurityGroupRulesInput{MaxResults: aws.Int32(sgRulePageSize)})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(CloudResource{}, fmt.Errorf("ec2:DescribeSecurityGroupRules: %w", err))
				return
			}
			rules := make([]CloudResource, 0, len(page.SecurityGroupRules))
			for i := range page.SecurityGroupRules {
				rules = append(rules, sgRuleResource(acct, region, &page.SecurityGroupRules[i]))
			}
			if !yieldAll(yield, rules, types) {
				return
			}
		}
	}
}

// sgRuleResource keeps a rule's identifiers and the hash of its tuple; the CIDRs and ports
// themselves aren't kept.
func sgRuleResource(acct *accounts.Account, region string, rule *ec2types.SecurityGroupRule) CloudResource {
	egress := aws.ToBool(rule.IsEgress)
	cfn := cfnSGIngress
	if egress {
		cfn = cfnSGEgress
	}
	group := aws.ToString(rule.GroupId)
	owner := cmp.Or(aws.ToString(rule.GroupOwnerId), acct.ID)
	key := tfstate.SGRule{
		GroupID: group, Egress: egress, Protocol: aws.ToString(rule.IpProtocol),
		FromPort: int(aws.ToInt32(rule.FromPort)), ToPort: int(aws.ToInt32(rule.ToPort)),
		Source: ruleSource(rule.CidrIpv4, rule.CidrIpv6, rule.PrefixListId, rule.ReferencedGroupInfo),
	}.Key()
	r := CloudResource{
		Account: owner, Region: region, CFNType: cfn,
		ARN: aws.ToString(rule.SecurityGroupRuleArn), ID: aws.ToString(rule.SecurityGroupRuleId),
		Attrs: map[string]string{"group_id": group, "rule_key": key},
		Parents: []Ref{{
			CFNType: cfnSecurityGroup, ID: group,
			ARN: fmt.Sprintf("arn:%s:ec2:%s:%s:security-group/%s", acct.Partition, region, owner, group),
		}},
		Sources: []string{LayerSGRules},
	}
	if c := ruleController(aws.ToString(rule.Description)); c != "" {
		r.setAttr("controller", c)
	}
	if ref := rule.ReferencedGroupInfo; ref != nil && aws.ToString(ref.GroupId) != "" {
		peer := aws.ToString(ref.GroupId)
		peerOwner := cmp.Or(aws.ToString(ref.UserId), owner)
		r.Relationships = append(r.Relationships, Ref{
			CFNType: cfnSecurityGroup, ID: peer, Relationship: "References",
			ARN: fmt.Sprintf("arn:%s:ec2:%s:%s:security-group/%s", acct.Partition, region, peerOwner, peer),
		})
	}
	for _, t := range rule.Tags {
		if r.Tags == nil {
			r.Tags = map[string]string{}
		}
		r.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return r
}

// ruleController names the Kubernetes controller that wrote a security group rule's description,
// or returns "". The description itself, which a person may have written, isn't kept.
func ruleController(description string) string {
	switch {
	case strings.HasPrefix(description, "elbv2.k8s.aws/"):
		return "aws-load-balancer-controller"
	case strings.HasPrefix(description, "kubernetes.io/rule/nlb/"):
		return "cloud-provider-aws"
	}
	return ""
}

// yieldAll yields the resources of the wanted types and reports whether to go on.
func yieldAll(yield func(CloudResource, error) bool, rs []CloudResource, types []string) bool {
	for _, r := range rs {
		if wanted(types, r.CFNType) && !yield(r, nil) {
			return false
		}
	}
	return true
}

func ruleSource(v4, v6, prefixList *string, group *ec2types.ReferencedSecurityGroup) string {
	switch {
	case aws.ToString(v4) != "":
		return "cidr4:" + aws.ToString(v4)
	case aws.ToString(v6) != "":
		return "cidr6:" + aws.ToString(v6)
	case aws.ToString(prefixList) != "":
		return "pl:" + aws.ToString(prefixList)
	case group != nil && aws.ToString(group.GroupId) != "":
		return "sg:" + aws.ToString(group.GroupId)
	}
	return ""
}

type natGateways struct{ env *Env }

func (*natGateways) Name() string               { return LayerNATGateways }
func (*natGateways) Global() bool               { return false }
func (*natGateways) Supports(Capabilities) bool { return true }
func (*natGateways) Types() []string            { return []string{cfnRouteTable} }

// natPageSize is the most NAT gateways DescribeNatGateways returns per page.
const natPageSize = 1000

// List reads the NAT gateways of region and yields the route table each regional NAT gateway
// came with, linked to the gateway. AWS Config doesn't record a NAT gateway's routeTableId.
func (l *natGateways) List(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		if !wanted(types, cfnRouteTable) {
			return
		}
		client := ec2.NewFromConfig(acct.Config, func(o *ec2.Options) {
			o.Region = region
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceEC2))
		})
		pages := ec2.NewDescribeNatGatewaysPaginator(client, &ec2.DescribeNatGatewaysInput{
			MaxResults: aws.Int32(natPageSize),
			Filter:     []ec2types.Filter{{Name: aws.String("state"), Values: []string{"pending", "available"}}},
		})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(CloudResource{}, fmt.Errorf("ec2:DescribeNatGateways: %w", err))
				return
			}
			for i := range page.NatGateways {
				if r, ok := natRouteTable(acct, region, &page.NatGateways[i]); ok && !yield(r, nil) {
					return
				}
			}
		}
	}
}

// natRouteTable returns the route table of a regional NAT gateway, or false for a zonal one.
func natRouteTable(acct *accounts.Account, region string, nat *ec2types.NatGateway) (CloudResource, bool) {
	rtb, id := aws.ToString(nat.RouteTableId), aws.ToString(nat.NatGatewayId)
	if rtb == "" {
		return CloudResource{}, false
	}
	arn := func(kind, id string) string {
		return fmt.Sprintf("arn:%s:ec2:%s:%s:%s/%s", acct.Partition, region, acct.ID, kind, id)
	}
	parent := Ref{CFNType: cfnNatGateway, ID: id, ARN: arn("natgateway", id)}
	rel := parent
	rel.Relationship = "Is the route table of"
	return CloudResource{
		Account: acct.ID, Region: region, CFNType: cfnRouteTable, ARN: arn("route-table", rtb), ID: rtb,
		Attrs: map[string]string{"regional_nat_gateway": id}, Parents: []Ref{parent}, Relationships: []Ref{rel},
		Sources: []string{LayerNATGateways},
	}, true
}

type iamAttachments struct{ env *Env }

func (*iamAttachments) Name() string               { return LayerIAMAttachments }
func (*iamAttachments) Global() bool               { return true }
func (*iamAttachments) Supports(Capabilities) bool { return true }
func (*iamAttachments) Types() []string {
	return []string{
		cfnRole,
		cfnUser,
		cfnGroup,
		cfnManagedPolicy,
		cfnRolePolicy,
		cfnUserPolicy,
		cfnGroupPolicy,
		cfnGroupAttachment,
		cfnRoleAttachment,
		cfnUserAttachment,
	}
}

// List reads every role, user, group and customer managed policy with
// GetAccountAuthorizationDetails, which returns the principals' attached and inline policies in
// one paginated call. It yields the principals and policies themselves, because AWS Config and
// Resource Explorer list IAM only in one region, which a scan may not cover. Policy documents
// come back too and are dropped: only names, IDs, ARNs, tags and dates are kept.
func (l *iamAttachments) List(
	ctx context.Context,
	acct *accounts.Account,
	_ string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		region := GlobalRegion(acct.Partition)
		client := iam.NewFromConfig(acct.Config, func(o *iam.Options) {
			o.Region = region
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceIAM))
		})
		pages := iam.NewGetAccountAuthorizationDetailsPaginator(client, &iam.GetAccountAuthorizationDetailsInput{
			Filter: []iamtypes.EntityType{
				iamtypes.EntityTypeRole, iamtypes.EntityTypeUser, iamtypes.EntityTypeGroup,
				iamtypes.EntityTypeLocalManagedPolicy,
			},
		})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(CloudResource{}, fmt.Errorf("iam:GetAccountAuthorizationDetails: %w", err))
				return
			}
			if !yieldAll(yield, iamPage(acct, page), types) {
				return
			}
		}
	}
}

func iamPage(acct *accounts.Account, page *iam.GetAccountAuthorizationDetailsOutput) []CloudResource {
	var out []CloudResource
	for _, d := range page.RoleDetailList {
		p := principal{"role", aws.ToString(d.RoleName), aws.ToString(d.Arn), cfnRole, cfnRoleAttachment, cfnRolePolicy}
		out = append(out, iamEntity(acct, cfnRole, d.Arn, d.RoleId, d.RoleName, d.CreateDate, d.Tags))
		out = append(out, p.resources(acct, d.AttachedManagedPolicies, d.RolePolicyList)...)
	}
	for _, d := range page.UserDetailList {
		p := principal{"user", aws.ToString(d.UserName), aws.ToString(d.Arn), cfnUser, cfnUserAttachment, cfnUserPolicy}
		out = append(out, iamEntity(acct, cfnUser, d.Arn, d.UserId, d.UserName, d.CreateDate, d.Tags))
		out = append(out, p.resources(acct, d.AttachedManagedPolicies, d.UserPolicyList)...)
	}
	for _, d := range page.GroupDetailList {
		p := principal{
			"group",
			aws.ToString(d.GroupName),
			aws.ToString(d.Arn),
			cfnGroup,
			cfnGroupAttachment,
			cfnGroupPolicy,
		}
		out = append(out, iamEntity(acct, cfnGroup, d.Arn, d.GroupId, d.GroupName, d.CreateDate, nil))
		out = append(out, p.resources(acct, d.AttachedManagedPolicies, d.GroupPolicyList)...)
	}
	for _, d := range page.Policies {
		out = append(out, iamEntity(acct, cfnManagedPolicy, d.Arn, d.PolicyId, d.PolicyName, d.CreateDate, nil))
	}
	return out
}

// iamEntity is a role, user, group or customer managed policy as the IAM lister keeps it.
func iamEntity(
	acct *accounts.Account,
	cfn string,
	arn, id, name *string,
	created *time.Time,
	tags []iamtypes.Tag,
) CloudResource {
	r := CloudResource{
		Account: acct.ID, CFNType: cfn, ARN: aws.ToString(arn), ID: aws.ToString(id), Name: aws.ToString(name),
		Sources: []string{LayerIAMAttachments},
	}
	if created != nil {
		r.Created = created.UTC().Format(time.RFC3339)
	}
	for _, t := range tags {
		if r.Tags == nil {
			r.Tags = map[string]string{}
		}
		r.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return r
}

type principal struct {
	kind, name, arn, cfn, attachment, inline string
}

// resources returns the principal's attachments and inline policy names.
func (p principal) resources(
	acct *accounts.Account,
	attached []iamtypes.AttachedPolicy,
	inline []iamtypes.PolicyDetail,
) []CloudResource {
	parent := []Ref{{CFNType: p.cfn, ID: p.name, ARN: p.arn}}
	var out []CloudResource
	for _, a := range attached {
		policy := aws.ToString(a.PolicyArn)
		out = append(out, CloudResource{
			Account: acct.ID, CFNType: p.attachment, ID: p.name + "/" + policy,
			Attrs: map[string]string{
				p.kind: p.name, "policy_arn": policy, "rule_key": tfstate.PolicyAttachmentKey(p.kind, p.name, policy),
			},
			Parents: parent,
			Sources: []string{LayerIAMAttachments},
		})
	}
	for _, pol := range inline {
		name := aws.ToString(pol.PolicyName)
		out = append(out, CloudResource{
			Account: acct.ID, CFNType: p.inline, ID: p.name + ":" + name, Name: name,
			Attrs: map[string]string{p.kind: p.name, attrName: name}, Parents: parent,
			Sources: []string{LayerIAMAttachments},
		})
	}
	return out
}

type route53Records struct{ env *Env }

func (*route53Records) Name() string               { return LayerRoute53Records }
func (*route53Records) Global() bool               { return true }
func (*route53Records) Supports(Capabilities) bool { return true }
func (*route53Records) Types() []string            { return []string{cfnRecordSet} }

func (l *route53Records) List(
	ctx context.Context,
	acct *accounts.Account,
	_ string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		if !wanted(types, cfnRecordSet) {
			return
		}
		region := GlobalRegion(acct.Partition)
		client := route53.NewFromConfig(acct.Config, func(o *route53.Options) {
			o.Region = region
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceRoute53))
		})
		zones := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{})
		for zones.HasMorePages() {
			page, err := zones.NextPage(ctx)
			if err != nil {
				yield(CloudResource{}, fmt.Errorf("route53:ListHostedZones: %w", err))
				return
			}
			for _, z := range page.HostedZones {
				id := strings.TrimPrefix(aws.ToString(z.Id), "/hostedzone/")
				zone := recordName(aws.ToString(z.Name))
				if !l.records(ctx, client, acct, id, zone, yield) {
					return
				}
			}
		}
	}
}

// records lists one zone with full pagination: a truncated page continues from its
// NextRecordName, NextRecordType and NextRecordIdentifier.
func (l *route53Records) records(
	ctx context.Context,
	client *route53.Client,
	acct *accounts.Account,
	zoneID, zone string,
	yield func(CloudResource, error) bool,
) bool {
	zoneARN := fmt.Sprintf("arn:%s:route53:::hostedzone/%s", acct.Partition, zoneID)
	pages := route53.NewListResourceRecordSetsPaginator(
		client,
		&route53.ListResourceRecordSetsInput{HostedZoneId: aws.String(zoneID)},
	)
	var zoneRecords []CloudResource
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return yield(CloudResource{}, fmt.Errorf("route53:ListResourceRecordSets %s: %w", zoneID, err))
		}
		for i := range page.ResourceRecordSets {
			zoneRecords = append(zoneRecords, recordResource(acct, zoneID, zone, zoneARN, &page.ResourceRecordSets[i]))
		}
	}
	// Ownership needs the whole zone: a TXT record and the record it owns can be on different pages.
	l.env.ExternalDNS.markExternalDNS(zoneRecords)
	for _, r := range zoneRecords {
		if !yield(r, nil) {
			return false
		}
	}
	return true
}

// recordResource keeps a record's identifiers. Of a TXT record's values it keeps only whether
// they mark external-dns ownership (txt_owner); no other record value is read.
func recordResource(
	acct *accounts.Account,
	zoneID, zone, zoneARN string,
	rs *r53types.ResourceRecordSet,
) CloudResource {
	name := recordName(aws.ToString(rs.Name))
	typ := string(rs.Type)
	set := aws.ToString(rs.SetIdentifier)
	short := ""
	if s, ok := strings.CutSuffix(name, "."+zone); ok {
		short = s
	}
	id := zoneID + "_" + name + "_" + typ
	if set != "" {
		id += "_" + set
	}
	r := CloudResource{
		Account: acct.ID, CFNType: cfnRecordSet, ID: id, Name: name,
		Attrs: map[string]string{
			"zone_id": zoneID, attrName: name, "type": typ, "set_identifier": set, "short_name": short,
			"apex": strconv.FormatBool(name == zone),
		},
		Parents: []Ref{{CFNType: cfnHostedZone, ID: zoneID, ARN: zoneARN}},
		Sources: []string{LayerRoute53Records},
	}
	if rs.Type == r53types.RRTypeTxt {
		if owner := txtOwner(rs.ResourceRecords); owner != "" {
			r.setAttr("txt_owner", owner)
		}
	}
	if rs.AliasTarget != nil {
		r.setAttr("alias", "true")
	}
	return r
}

// policySids returns the statement IDs of a bucket policy, sorted and joined by commas; a
// statement without one adds an empty ID. Nothing else of the policy is kept.
func policySids(policy string) string {
	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if json.Unmarshal([]byte(policy), &doc) != nil {
		return ""
	}
	type statement struct {
		Sid string `json:"Sid"`
	}
	var list []statement
	if json.Unmarshal(doc.Statement, &list) != nil {
		var one statement
		if json.Unmarshal(doc.Statement, &one) != nil {
			return ""
		}
		list = []statement{one}
	}
	sids := make([]string, 0, len(list))
	for _, st := range list {
		sids = append(sids, st.Sid)
	}
	slices.Sort(sids)
	return strings.Join(sids, ",")
}

// recordName returns a Route 53 name the way the provider stores fqdn: lower case, without the
// trailing dot, with octal escapes such as \052 (an asterisk) decoded.
func recordName(s string) string {
	s = strings.TrimSuffix(strings.ToLower(s), ".")
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

type s3Subresources struct{ env *Env }

func (*s3Subresources) Name() string               { return LayerS3Subresources }
func (*s3Subresources) Global() bool               { return false }
func (*s3Subresources) Supports(Capabilities) bool { return true }
func (*s3Subresources) Types() []string {
	return []string{cfnBucketEncrypt, cfnBucketLifecycle, cfnBucketPAB, cfnBucketVersions, cfnBucketPolicy}
}

// List reads the buckets in region (ListBuckets filtered by BucketRegion, so each region's call
// stands alone) and then each bucket's policy, versioning, lifecycle, encryption and public
// access block. A configuration a bucket doesn't have is not a resource.
func (l *s3Subresources) List(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		client := s3.NewFromConfig(acct.Config, func(o *s3.Options) {
			o.Region = region
			o.UsePathStyle = l.env.S3PathStyle
			o.DisableLogOutputChecksumValidationSkipped = true
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceS3))
		})
		pages := s3.NewListBucketsPaginator(client, &s3.ListBucketsInput{BucketRegion: aws.String(region)})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(CloudResource{}, fmt.Errorf("s3:ListBuckets: %w", err))
				return
			}
			for _, b := range page.Buckets {
				if !l.bucket(ctx, client, acct, region, aws.ToString(b.Name), types, yield) {
					return
				}
			}
		}
	}
}

// bucketCheck reads one bucket configuration. It returns false when the bucket doesn't have it,
// and the attributes rules read from it.
type bucketCheck struct {
	cfn, action string
	absent      []string // error codes that mean the configuration isn't set
	read        func(ctx context.Context, c *s3.Client, bucket string) (bool, map[string]string, error)
}

// noAttrs adapts a check that only reports whether the configuration is set.
func noAttrs(ok bool, err error) (bool, map[string]string, error) { return ok, nil, err }

func bucketChecks() []bucketCheck {
	b := aws.String
	return []bucketCheck{
		{
			cfnBucketPolicy, "s3:GetBucketPolicy",
			[]string{"NoSuchBucketPolicy"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, map[string]string, error) {
				out, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: b(bucket)})
				if err != nil {
					return false, nil, err
				}
				if sids := policySids(aws.ToString(out.Policy)); sids != "" {
					return true, map[string]string{"policy_sids": sids}, nil
				}
				return true, nil, nil
			},
		},
		{
			cfnBucketVersions, "s3:GetBucketVersioning", nil,
			func(ctx context.Context, c *s3.Client, bucket string) (bool, map[string]string, error) {
				out, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: b(bucket)})
				return noAttrs(err == nil && out.Status != "", err)
			},
		},
		{
			cfnBucketLifecycle, "s3:GetLifecycleConfiguration",
			[]string{"NoSuchLifecycleConfiguration"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, map[string]string, error) {
				_, err := c.GetBucketLifecycleConfiguration(
					ctx,
					&s3.GetBucketLifecycleConfigurationInput{Bucket: b(bucket)},
				)
				return noAttrs(err == nil, err)
			},
		},
		{
			cfnBucketEncrypt, "s3:GetEncryptionConfiguration",
			[]string{"ServerSideEncryptionConfigurationNotFoundError"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, map[string]string, error) {
				_, err := c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: b(bucket)})
				return noAttrs(err == nil, err)
			},
		},
		{
			cfnBucketPAB, "s3:GetBucketPublicAccessBlock",
			[]string{"NoSuchPublicAccessBlockConfiguration"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, map[string]string, error) {
				_, err := c.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: b(bucket)})
				return noAttrs(err == nil, err)
			},
		},
	}
}

func (l *s3Subresources) bucket(
	ctx context.Context,
	c *s3.Client,
	acct *accounts.Account,
	region, bucket string,
	types []string,
	yield func(CloudResource, error) bool,
) bool {
	parent := []Ref{{CFNType: cfnBucket, ID: bucket, ARN: "arn:" + acct.Partition + ":s3:::" + bucket}}
	for _, check := range bucketChecks() {
		if !wanted(types, check.cfn) {
			continue
		}
		ok, attrs, err := check.read(ctx, c, bucket)
		if err != nil && !slices.Contains(check.absent, apiCode(err)) {
			if ctx.Err() != nil {
				return false
			}
			if !yield(CloudResource{}, fmt.Errorf("%s %s: %w", check.action, bucket, err)) {
				return false
			}
			continue
		}
		if !ok {
			continue
		}
		r := CloudResource{
			Account: acct.ID, Region: region, CFNType: check.cfn, ID: bucket,
			Attrs: map[string]string{"bucket": bucket}, Parents: parent, Sources: []string{LayerS3Subresources},
		}
		for k, v := range attrs {
			r.setAttr(k, v)
		}
		if !yield(r, nil) {
			return false
		}
	}
	return true
}
