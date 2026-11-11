package typemap_test

import (
	"slices"
	"testing"

	cstypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/typemap"
)

func load(t *testing.T) *typemap.Map {
	t.Helper()
	m, err := typemap.Load()
	require.NoError(t, err)
	return m
}

func TestCurated(t *testing.T) {
	t.Parallel()

	m := load(t)
	curated := m.Curated()
	assert.GreaterOrEqual(t, len(curated), 40)
	configTypes := cstypes.ResourceType("").Values()
	natives := []string{"ec2-sg-rules", "iam-attachments", "route53-records", "s3-subresources"}
	for _, ty := range curated {
		if ty.Config != "" {
			assert.Contains(
				t,
				configTypes,
				cstypes.ResourceType(ty.Config),
				"%s: AWS Config doesn't record %s",
				ty.TF,
				ty.Config,
			)
		}
		if ty.Native != "" {
			assert.Contains(t, natives, ty.Native, ty.TF)
		}
		if ty.Derived != nil {
			assert.True(t, m.IsCFN(ty.Derived.Parent), ty.TF)
		}
		hasKey := ty.Identity != "" || ty.ARN != nil || len(ty.ID) > 0 || ty.Tuple
		assert.True(t, hasKey, "%s has nothing to match on", ty.TF)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	m := load(t)
	tests := []struct {
		arn, cfn string
		fields   map[string]string
	}{
		{"arn:aws:ec2:eu-west-1:111111111111:vpc/vpc-0a1", "AWS::EC2::VPC", map[string]string{"id": "vpc-0a1"}},
		{"arn:aws:iam::111111111111:role/service-role/app", "AWS::IAM::Role", map[string]string{"name": "app"}},
		{"arn:aws:iam::111111111111:role/app", "AWS::IAM::Role", map[string]string{"name": "app"}},
		{"arn:aws:s3:::acme-logs", "AWS::S3::Bucket", map[string]string{"bucket": "acme-logs"}},
		{"arn:aws:kms:eu-west-1:111111111111:alias/app", "AWS::KMS::Alias", map[string]string{"name": "alias/app"}},
		{"arn:aws:kms:eu-west-1:111111111111:key/1234", "AWS::KMS::Key", map[string]string{"id": "1234"}},
		{
			"arn:aws:logs:eu-west-1:111111111111:log-group:/aws/lambda/fn:*",
			"AWS::Logs::LogGroup",
			map[string]string{"name": "/aws/lambda/fn"},
		},
		{
			"arn:aws:ecr:eu-west-1:111111111111:repository/team/app",
			"AWS::ECR::Repository",
			map[string]string{"name": "team/app"},
		},
		{
			"arn:aws:elasticloadbalancing:eu-west-1:111111111111:listener/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2",
			"AWS::ElasticLoadBalancingV2::Listener",
			map[string]string{"lb_name": "web"},
		},
		{"arn:aws:sns:eu-west-1:111111111111:alerts:0f5a-uuid", "", nil},
		{"arn:aws:dynamodb:eu-west-1:111111111111:table/t/stream/2026", "", nil},
		{"arn:aws:ec2:eu-west-1:111111111111:security-group-rule/sgr-1", "", nil},
	}
	for _, tt := range tests {
		cfn, fields := m.Classify(tt.arn)
		assert.Equal(t, tt.cfn, cfn, tt.arn)
		for k, v := range tt.fields {
			assert.Equal(t, v, fields[k], "%s: %s", tt.arn, k)
		}
	}
}

func TestLookups(t *testing.T) {
	t.Parallel()

	m := load(t)
	tfs := func(cfn string) []string {
		var out []string
		for _, ty := range m.ForCFN(cfn) {
			out = append(out, ty.TF)
		}
		return out
	}
	assert.Equal(
		t,
		[]string{"aws_security_group_rule", "aws_vpc_security_group_ingress_rule"},
		tfs("AWS::EC2::SecurityGroupIngress"),
	)
	assert.Equal(t, []string{"aws_alb", "aws_lb"}, tfs("AWS::ElasticLoadBalancingV2::LoadBalancer"))
	assert.Equal(t, "AWS::IAM::ManagedPolicy", m.FromConfig("AWS::IAM::Policy"))
	assert.Equal(t, "AWS::EC2::VPC", m.FromConfig("AWS::EC2::VPC"))
	assert.Equal(t, "AWS::EC2::VPC", m.FromRE("ec2:vpc"))
	assert.Equal(t, "AWS::IAM::ManagedPolicy", m.FromRE("iam:policy"))
	assert.Empty(t, m.FromRE("nope:thing"))
	assert.True(t, m.HasRE("AWS::EC2::SecurityGroup"))
	assert.False(t, m.Mapped("AWS::Glue::Job"))
	assert.Equal(
		t,
		[]string{"AWS::EC2::VPC", "AWS::IAM::Policy"},
		m.ConfigTypes([]string{"AWS::IAM::ManagedPolicy", "AWS::EC2::VPC"}),
	)
	all := m.ConfigTypes(nil)
	assert.True(t, slices.IsSorted(all))
	assert.NotContains(t, all, "AWS::Route53::RecordSet")

	role, ok := m.Type("aws_iam_role")
	require.True(t, ok)
	assert.True(t, role.Global)
	assert.Equal(t, []string{"account_id", "name"}, role.IdentityAttrs)
	rec, _ := m.Type("aws_route53_record")
	assert.Equal(t, "fqdn", rec.IdentityFrom["name"])
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	gen := []byte("sources: []\nresources:\n  - tf: aws_vpc\n    service: ec2\ncfn_types: [AWS::EC2::VPC]\n")
	tests := map[string]string{
		"unknown tf":    "- tf: aws_nope\n  cfn: AWS::EC2::VPC\n  id: ['{id}']\n",
		"unknown cfn":   "- tf: aws_vpc\n  cfn: AWS::EC2::Nope\n  id: ['{id}']\n",
		"no cfn":        "- tf: aws_vpc\n  id: ['{id}']\n",
		"no key":        "- tf: aws_vpc\n  cfn: AWS::EC2::VPC\n",
		"twice":         "- tf: aws_vpc\n  cfn: AWS::EC2::VPC\n  id: ['{id}']\n- tf: aws_vpc\n  cfn: AWS::EC2::VPC\n  id: ['{id}']\n",
		"unknown field": "- tf: aws_vpc\n  cfn: AWS::EC2::VPC\n  idd: ['{id}']\n",
		"bad pattern":   "- tf: aws_vpc\n  cfn: AWS::EC2::VPC\n  arn: 'arn:{x:(}'\n",
	}
	for name, cur := range tests {
		_, err := typemap.Parse(gen, []byte(cur))
		require.Error(t, err, name)
	}
	_, err := typemap.Parse(gen, []byte("- tf: aws_vpc\n  cfn: [AWS::EC2::VPC]\n  id: ['{id}']\n"))
	require.NoError(t, err)
}

func TestTemplate(t *testing.T) {
	t.Parallel()

	fields := map[string]string{"zone_id": "Z1", "name": "www.example.com", "type": "A"}
	lookup := func(k string) string { return fields[k] }
	tests := []struct {
		tmpl typemap.Template
		want string
		ok   bool
	}{
		{"{zone_id}_{name}_{type}[_{set_identifier}]", "Z1_www.example.com_A", true},
		{"{zone_id}_{missing}", "", false},
		{"[{missing}-]{name}", "www.example.com", true},
		{"plain", "plain", true},
		{"{zone_id}[_{name}", "", false},
	}
	for _, tt := range tests {
		got, ok := tt.tmpl.Render(lookup)
		assert.Equal(t, tt.ok, ok, tt.tmpl)
		assert.Equal(t, tt.want, got, tt.tmpl)
	}
	fields["set_identifier"] = "blue"
	got, _ := typemap.Template("{zone_id}_{name}_{type}[_{set_identifier}]").Render(lookup)
	assert.Equal(t, "Z1_www.example.com_A_blue", got)
	assert.Equal(t, []string{"zone_id", "name"}, typemap.Template("{zone_id}[_{name}]").Fields())
}

func TestPattern(t *testing.T) {
	t.Parallel()

	p, err := typemap.CompilePattern("arn:{partition}:iam::{account}:role/{path:(?:[^:]*/)?}{name}")
	require.NoError(t, err)
	assert.Equal(t, "arn:{partition}:iam::{account}:role/{path:(?:[^:]*/)?}{name}", p.String())
	got := p.Match("arn:aws-us-gov:iam::111111111111:role/a/b/app")
	assert.Equal(
		t,
		map[string]string{"partition": "aws-us-gov", "account": "111111111111", "path": "a/b/", "name": "app"},
		got,
	)
	assert.Nil(t, p.Match("arn:aws:iam::111111111111:user/app"))
	assert.Equal(t, "arn:aws:logs:r:a:log-group:x", typemap.NormalizeARN("arn:aws:logs:r:a:log-group:x:*"))
	assert.Equal(t, "arn:aws:s3:::b:*", typemap.NormalizeARN("arn:aws:s3:::b:*"))
}
