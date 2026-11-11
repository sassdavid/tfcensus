package inventory

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"slices"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
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
	cfnRoleAttachment  = typemap.NativePrefix + "IAM::RolePolicyAttachment"
	cfnUserAttachment  = typemap.NativePrefix + "IAM::UserPolicyAttachment"
	cfnGroupAttachment = typemap.NativePrefix + "IAM::GroupPolicyAttachment"
	cfnRolePolicy      = "AWS::IAM::RolePolicy"
	cfnUserPolicy      = "AWS::IAM::UserPolicy"
	cfnGroupPolicy     = "AWS::IAM::GroupPolicy"
	cfnRole            = "AWS::IAM::Role"
	cfnUser            = "AWS::IAM::User"
	cfnGroup           = "AWS::IAM::Group"
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
	return []Layer{&sgRules{env}, &iamAttachments{env}, &route53Records{env}, &s3Subresources{env}}
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
	for _, t := range rule.Tags {
		if r.Tags == nil {
			r.Tags = map[string]string{}
		}
		r.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return r
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

type iamAttachments struct{ env *Env }

func (*iamAttachments) Name() string               { return LayerIAMAttachments }
func (*iamAttachments) Global() bool               { return true }
func (*iamAttachments) Supports(Capabilities) bool { return true }
func (*iamAttachments) Types() []string {
	return []string{
		cfnRolePolicy,
		cfnUserPolicy,
		cfnGroupPolicy,
		cfnGroupAttachment,
		cfnRoleAttachment,
		cfnUserAttachment,
	}
}

// List reads every role, user and group with GetAccountAuthorizationDetails, which returns
// their attached and inline policies in one paginated call. Policy documents come back too and
// are dropped: only names and ARNs are kept.
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
			Filter: []iamtypes.EntityType{iamtypes.EntityTypeRole, iamtypes.EntityTypeUser, iamtypes.EntityTypeGroup},
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
		out = append(out, p.resources(acct, d.AttachedManagedPolicies, d.RolePolicyList)...)
	}
	for _, d := range page.UserDetailList {
		p := principal{"user", aws.ToString(d.UserName), aws.ToString(d.Arn), cfnUser, cfnUserAttachment, cfnUserPolicy}
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
		out = append(out, p.resources(acct, d.AttachedManagedPolicies, d.GroupPolicyList)...)
	}
	return out
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
			Attrs: map[string]string{p.kind: p.name, "policy_arn": policy}, Parents: parent,
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
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return yield(CloudResource{}, fmt.Errorf("route53:ListResourceRecordSets %s: %w", zoneID, err))
		}
		for _, rs := range page.ResourceRecordSets {
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
			if !yield(r, nil) {
				return false
			}
		}
	}
	return true
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

// bucketCheck reads one bucket configuration. It returns false when the bucket doesn't have it.
type bucketCheck struct {
	cfn, action string
	absent      []string // error codes that mean the configuration isn't set
	read        func(ctx context.Context, c *s3.Client, bucket string) (bool, error)
}

func bucketChecks() []bucketCheck {
	b := aws.String
	return []bucketCheck{
		{
			cfnBucketPolicy, "s3:GetBucketPolicy",
			[]string{"NoSuchBucketPolicy"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, error) {
				_, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: b(bucket)})
				return err == nil, err
			},
		},
		{
			cfnBucketVersions, "s3:GetBucketVersioning", nil,
			func(ctx context.Context, c *s3.Client, bucket string) (bool, error) {
				out, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: b(bucket)})
				return err == nil && out.Status != "", err
			},
		},
		{
			cfnBucketLifecycle, "s3:GetLifecycleConfiguration",
			[]string{"NoSuchLifecycleConfiguration"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, error) {
				_, err := c.GetBucketLifecycleConfiguration(
					ctx,
					&s3.GetBucketLifecycleConfigurationInput{Bucket: b(bucket)},
				)
				return err == nil, err
			},
		},
		{
			cfnBucketEncrypt, "s3:GetEncryptionConfiguration",
			[]string{"ServerSideEncryptionConfigurationNotFoundError"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, error) {
				_, err := c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: b(bucket)})
				return err == nil, err
			},
		},
		{
			cfnBucketPAB, "s3:GetBucketPublicAccessBlock",
			[]string{"NoSuchPublicAccessBlockConfiguration"},
			func(ctx context.Context, c *s3.Client, bucket string) (bool, error) {
				_, err := c.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: b(bucket)})
				return err == nil, err
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
		found, err := check.read(ctx, c, bucket)
		if err != nil && !slices.Contains(check.absent, apiCode(err)) {
			if ctx.Err() != nil {
				return false
			}
			if !yield(CloudResource{}, fmt.Errorf("%s %s: %w", check.action, bucket, err)) {
				return false
			}
			continue
		}
		if !found {
			continue
		}
		r := CloudResource{
			Account: acct.ID, Region: region, CFNType: check.cfn, ID: bucket,
			Attrs: map[string]string{"bucket": bucket}, Parents: parent, Sources: []string{LayerS3Subresources},
		}
		if !yield(r, nil) {
			return false
		}
	}
	return true
}
