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
	assert.GreaterOrEqual(t, len(curated), 130)
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
		"unlisted with config": "- tf: aws_vpc\n  cfn: AWS::EC2::VPC\n  config: AWS::EC2::VPC\n  id: ['{id}']\n" +
			"  unlisted: nothing lists it\n",
		"cc with re": "- tf: aws_vpc\n  cfn: AWS::EC2::VPC\n  re: ec2:vpc\n  cc: true\n  id: ['{id}']\n",
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

// validationTypes are the Terraform types the first real-account scan used, from the table in
// docs/plan/specs/scan-accuracy.md, plus the aws_default_* types and aws_iam_policy_attachment.
func validationTypes() []string {
	return []string{
		"aws_vpc_endpoint", "aws_vpc_endpoint_service", "aws_route_table_association",
		"aws_network_acl_association", "aws_vpc_ipv4_cidr_block_association", "aws_vpc_peering_connection",
		"aws_vpc_peering_connection_accepter", "aws_flow_log", "aws_ec2_managed_prefix_list",
		"aws_ec2_client_vpn_endpoint", "aws_customer_gateway", "aws_vpn_gateway", "aws_vpn_connection",
		"aws_eip_association",
		"aws_vpclattice_resource_gateway", "aws_vpclattice_resource_configuration", "aws_vpclattice_service_network",
		"aws_vpclattice_service_network_resource_association", "aws_vpclattice_service_network_vpc_association",
		"aws_eks_node_group", "aws_eks_addon", "aws_eks_access_entry", "aws_eks_pod_identity_association",
		"aws_iam_openid_connect_provider",
		"aws_lambda_event_source_mapping", "aws_lambda_layer_version", "aws_cloudwatch_event_rule",
		"aws_cloudwatch_event_bus", "aws_pipes_pipe", "aws_scheduler_schedule_group",
		"aws_msk_cluster", "aws_msk_configuration", "aws_opensearch_domain", "aws_elasticache_replication_group",
		"aws_elasticache_user", "aws_elasticache_user_group", "aws_memorydb_acl", "aws_memorydb_user",
		"aws_memorydb_parameter_group", "aws_db_parameter_group", "aws_db_option_group", "aws_efs_file_system",
		"aws_efs_access_point", "aws_efs_mount_target", "aws_glue_catalog_database", "aws_glue_catalog_table",
		"aws_athena_workgroup", "aws_athena_data_catalog",
		"aws_api_gateway_rest_api", "aws_api_gateway_resource", "aws_api_gateway_method",
		"aws_api_gateway_deployment", "aws_api_gateway_stage", "aws_api_gateway_domain_name",
		"aws_api_gateway_base_path_mapping",
		"aws_ssm_parameter", "aws_ssm_association", "aws_cloudwatch_metric_alarm", "aws_cloudwatch_dashboard",
		"aws_acm_certificate", "aws_codebuild_project", "aws_codepipeline", "aws_kinesisanalyticsv2_application",
		"aws_backup_plan", "aws_backup_vault", "aws_backup_selection", "aws_cloudformation_stack",
		"aws_quicksight_data_source", "aws_quicksight_vpc_connection", "aws_route53_resolver_rule",
		"aws_route53_resolver_rule_association", "aws_xray_group", "aws_xray_sampling_rule",
		"aws_resourceexplorer2_index", "aws_resourceexplorer2_view",
		"aws_apprunner_auto_scaling_configuration_version",
		"aws_default_route_table", "aws_default_security_group", "aws_default_network_acl",
		"aws_default_vpc_dhcp_options", "aws_iam_policy_attachment",
	}
}

// TestSpec_scan_accuracy_5: every type the validation account uses is curated, and a layer lists
// it unless its entry says why none can.
func TestSpec_scan_accuracy_5(t *testing.T) {
	t.Parallel()

	m := load(t)
	for _, tf := range validationTypes() {
		ty, ok := m.Type(tf)
		require.True(t, ok, tf)
		if !assert.True(t, ty.Curated, "%s is not curated", tf) {
			continue
		}
		listed := ty.Config != "" || len(ty.RE) > 0 || ty.CC || ty.Native != ""
		assert.True(t, listed || ty.Unlisted != "", "%s: no layer lists it and its entry doesn't say why", tf)
		assert.False(t, listed && ty.Unlisted != "", "%s is listed and unlisted", tf)
	}
	assert.Equal(t, "AWS::OpenSearchService::Domain", m.FromConfig("AWS::OpenSearch::Domain"))
	assert.Equal(t, "AWS::OpenSearchService::Domain", m.FromRE("es:domain"))
	cfn, _ := m.Classify("arn:aws:es:eu-west-1:111111111111:domain/search")
	assert.Equal(t, "AWS::OpenSearchService::Domain", cfn, "one entry for both names")
	assert.Equal(t, []string{
		"AWS::VpcLattice::ResourceConfiguration", "AWS::VpcLattice::ResourceGateway",
		"AWS::VpcLattice::ServiceNetworkResourceAssociation", "AWS::VpcLattice::ServiceNetworkVpcAssociation",
	}, m.CCTypes())
	vpcAssoc, _ := m.Type("aws_vpclattice_service_network_vpc_association")
	assert.Empty(t, vpcAssoc.RE, "a cc type drops its Resource Explorer types")
}

// TestClassifyCurated: the ARN patterns of the newer entries give the fields their id templates
// read.
func TestClassifyCurated(t *testing.T) {
	t.Parallel()

	m := load(t)
	tests := []struct {
		arn, cfn string
		fields   map[string]string
	}{
		{
			"arn:aws:events:eu-west-1:111111111111:rule/orders/nightly", "AWS::Events::Rule",
			map[string]string{"bus": "orders/", "name": "nightly"},
		},
		{
			"arn:aws:events:eu-west-1:111111111111:rule/nightly",
			"AWS::Events::Rule",
			map[string]string{"name": "nightly"},
		},
		{
			"arn:aws:apigateway:eu-west-1::/restapis/a1b2c3/resources/r4s5/methods/GET", "AWS::ApiGateway::Method",
			map[string]string{"rest_api_id": "a1b2c3", "resource_id": "r4s5", "http_method": "GET"},
		},
		{
			"arn:aws:apigateway:eu-west-1::/restapis/a1b2c3",
			"AWS::ApiGateway::RestApi",
			map[string]string{"id": "a1b2c3"},
		},
		{
			"arn:aws:ssm:eu-west-1:111111111111:parameter/app/db/host", "AWS::SSM::Parameter",
			map[string]string{"name": "app/db/host"},
		},
		{
			"arn:aws:lambda:eu-west-1:111111111111:layer:deps:7", "AWS::Lambda::LayerVersion",
			map[string]string{"layer_name": "deps", "version": "7"},
		},
		{"arn:aws:lambda:eu-west-1:111111111111:function:orders:7", "", nil},
		{
			"arn:aws:eks:eu-west-1:111111111111:podidentityassociation/main/a-0123456789abcdef0",
			"AWS::EKS::PodIdentityAssociation",
			map[string]string{"association_id": "a-0123456789abcdef0"},
		},
		{
			"arn:aws:cloudwatch::111111111111:dashboard/ops", "AWS::CloudWatch::Dashboard",
			map[string]string{"dashboard_name": "ops"},
		},
		{
			"arn:aws:vpc-lattice:eu-west-1:111111111111:resourceconfiguration/rcfg-0123456789abcdef0",
			"AWS::VpcLattice::ResourceConfiguration",
			map[string]string{"id": "rcfg-0123456789abcdef0"},
		},
		{"arn:aws:acm:eu-west-1:111111111111:certificate/0f1e2d3c", "AWS::ACM::Certificate", nil},
	}
	for _, tt := range tests {
		cfn, fields := m.Classify(tt.arn)
		assert.Equal(t, tt.cfn, cfn, tt.arn)
		for k, v := range tt.fields {
			assert.Equal(t, v, fields[k], "%s: %s", tt.arn, k)
		}
	}
	dash, _ := m.Type("aws_cloudwatch_dashboard")
	assert.True(t, dash.Global, "curated global overrides the provider's region")
}

// TestGeneratedAliases: the generator's alias table finds the CloudFormation candidate of the
// families the aws and awscc providers name differently.
func TestGeneratedAliases(t *testing.T) {
	t.Parallel()

	m := load(t)
	for tf, cfn := range map[string]string{
		"aws_cloudwatch_event_rule":   "AWS::Events::Rule",
		"aws_cloudwatch_metric_alarm": "AWS::CloudWatch::Alarm",
		"aws_api_gateway_rest_api":    "AWS::ApiGateway::RestApi",
		"aws_opensearch_domain":       "AWS::OpenSearchService::Domain",
		"aws_codepipeline":            "AWS::CodePipeline::Pipeline",
	} {
		ty, ok := m.Type(tf)
		require.True(t, ok, tf)
		assert.Equal(t, cfn, ty.GenResource.CFN, tf)
	}
	webhook, ok := m.Type("aws_codepipeline_webhook")
	require.True(t, ok)
	assert.NotEqual(t, "AWS::CodePipeline::Pipeline", webhook.GenResource.CFN, "a whole-name alias isn't a prefix")
}
