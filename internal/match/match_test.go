package match_test

import (
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	acct   = "111111111111"
	region = "eu-west-1"
)

func arnOf(service, resource string) string {
	return "arn:aws:" + service + ":" + region + ":" + acct + ":" + resource
}

func iamARN(resource string) string { return "arn:aws:iam::" + acct + ":" + resource }

func loadMap(t *testing.T) *typemap.Map {
	t.Helper()
	m, err := typemap.Load()
	require.NoError(t, err)
	return m
}

// unitState wraps instances of one type in a unit whose target is acct and region.
func unitState(unit, tf string, insts ...tfstate.Instance) tfstate.UnitState {
	return tfstate.UnitState{
		Repo: "live", Path: unit, Workspace: tfstate.DefaultWorkspace, Status: tfstate.StatusOK,
		Target: discovery.Target{Account: acct, Region: region},
		State: &tfstate.State{Version: 4, Resources: []tfstate.Resource{{
			Mode: tfstate.ModeManaged, Type: tf, Name: "this",
			Provider: tfstate.Provider{Source: "hashicorp/aws"}, Instances: insts,
		}}},
	}
}

func live(cfn string, f func(*inventory.CloudResource)) inventory.CloudResource {
	r := inventory.CloudResource{Account: acct, Region: region, CFNType: cfn, Sources: []string{inventory.LayerConfig}}
	f(&r)
	return r
}

type typeCase struct {
	tf    string
	live  inventory.CloudResource
	state tfstate.Instance // as the provider writes it, with identity when the type has one
	noID  bool             // the TF id can't be rebuilt from the cloud (a random suffix)
}

// typeCases cover the curated types: VPC, subnets, security groups and rules, IAM, S3 and its
// subresources, KMS, RDS, EKS, ELBv2, Route 53, Lambda, SQS, SNS, ECR, Secrets Manager,
// CloudWatch Logs and DynamoDB.
func typeCases() []typeCase {
	ident := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	regional := func(kv ...string) map[string]string {
		return ident(append([]string{"account_id", acct, "region", region}, kv...)...)
	}
	global := func(kv ...string) map[string]string { return ident(append([]string{"account_id", acct}, kv...)...) }
	ec2 := func(tf, cfn, kind, id string) typeCase {
		a := arnOf("ec2", kind+"/"+id)
		return typeCase{
			tf: tf, live: live(cfn, func(r *inventory.CloudResource) { r.ARN, r.ID = a, id }),
			state: tfstate.Instance{ID: id, ARN: a, Identity: regional("id", id)},
		}
	}
	lb := func(tf, cfn, a string) typeCase {
		return typeCase{
			tf: tf, live: live(cfn, func(r *inventory.CloudResource) { r.ARN = a }),
			state: tfstate.Instance{ID: a, ARN: a, Identity: ident("arn", a)},
		}
	}
	sgr := func(tf, cfn string) typeCase {
		a := arnOf("ec2", "security-group-rule/sgr-1")
		return typeCase{tf: tf, live: live(cfn, func(r *inventory.CloudResource) {
			r.ARN, r.ID, r.Sources = a, "sgr-1", []string{inventory.LayerSGRules}
			r.Attrs = map[string]string{"group_id": "sg-1", "rule_key": "sgr:unused"}
		}), state: tfstate.Instance{ID: "sgr-1", ARN: a, Identity: regional("id", "sgr-1")}}
	}
	native := func(tf, cfn, id string, attrs map[string]string, inst tfstate.Instance) typeCase {
		return typeCase{tf: tf, live: live(cfn, func(r *inventory.CloudResource) {
			r.Region, r.ID, r.Attrs, r.Sources = "", id, attrs, []string{inventory.LayerIAMAttachments}
		}), state: inst}
	}
	bucketSub := func(tf, cfn string) typeCase {
		return typeCase{tf: tf, live: live(cfn, func(r *inventory.CloudResource) {
			r.ID, r.Attrs, r.Sources = "acme-logs", map[string]string{
				"bucket": "acme-logs",
			}, []string{
				inventory.LayerS3Subresources,
			}
		}), state: tfstate.Instance{ID: "acme-logs", Region: region, Identity: regional("bucket", "acme-logs")}}
	}
	policy := "arn:aws:iam::aws:policy/ReadOnlyAccess"
	lbARN := arnOf("elasticloadbalancing", "loadbalancer/app/web/50dc6c495c0c9188")
	listenerARN := arnOf("elasticloadbalancing", "listener/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2")
	tgARN := arnOf("elasticloadbalancing", "targetgroup/web/73e2d6bc24d8a067")
	queueURL := "https://sqs." + region + ".amazonaws.com/" + acct + "/jobs"
	return []typeCase{
		ec2("aws_vpc", "AWS::EC2::VPC", "vpc", "vpc-1"),
		ec2("aws_subnet", "AWS::EC2::Subnet", "subnet", "subnet-1"),
		ec2("aws_security_group", "AWS::EC2::SecurityGroup", "security-group", "sg-1"),
		ec2("aws_internet_gateway", "AWS::EC2::InternetGateway", "internet-gateway", "igw-1"),
		ec2("aws_route_table", "AWS::EC2::RouteTable", "route-table", "rtb-1"),
		ec2("aws_eip", "AWS::EC2::EIP", "elastic-ip", "eipalloc-1"),
		{tf: "aws_nat_gateway", live: live("AWS::EC2::NatGateway", func(r *inventory.CloudResource) {
			r.ARN, r.ID = arnOf("ec2", "natgateway/nat-1"), "nat-1"
		}), state: tfstate.Instance{ID: "nat-1", Identity: regional("id", "nat-1")}},
		sgr("aws_vpc_security_group_ingress_rule", "AWS::EC2::SecurityGroupIngress"),
		sgr("aws_vpc_security_group_egress_rule", "AWS::EC2::SecurityGroupEgress"),
		{tf: "aws_iam_role", live: live("AWS::IAM::Role", func(r *inventory.CloudResource) {
			r.Region, r.ARN, r.ID, r.Name = "", iamARN("role/service-role/app"), "AROAEXAMPLE", "app"
		}), state: tfstate.Instance{ID: "app", ARN: iamARN("role/service-role/app"), Identity: global("name", "app")}},
		{tf: "aws_iam_user", live: live("AWS::IAM::User", func(r *inventory.CloudResource) {
			r.Region, r.ARN, r.ID = "", iamARN("user/ci"), "AIDAEXAMPLE"
		}), state: tfstate.Instance{ID: "ci", ARN: iamARN("user/ci"), Identity: global("name", "ci")}},
		{tf: "aws_iam_group", live: live("AWS::IAM::Group", func(r *inventory.CloudResource) {
			r.Region, r.ARN = "", iamARN("group/ops")
		}), state: tfstate.Instance{ID: "ops", ARN: iamARN("group/ops"), Identity: global("name", "ops")}},
		{tf: "aws_iam_policy", live: live("AWS::IAM::ManagedPolicy", func(r *inventory.CloudResource) {
			r.Region, r.ARN, r.ID = "", iamARN("policy/app-read"), "ANPAEXAMPLE"
		}), state: tfstate.Instance{ID: iamARN("policy/app-read"), ARN: iamARN("policy/app-read"), Identity: ident("arn", iamARN("policy/app-read"))}},
		{tf: "aws_iam_instance_profile", live: live("AWS::IAM::InstanceProfile", func(r *inventory.CloudResource) {
			r.Region, r.ARN = "", iamARN("instance-profile/app")
		}), state: tfstate.Instance{ID: "app", ARN: iamARN("instance-profile/app"), Identity: global("name", "app")}},
		native("aws_iam_role_policy_attachment", "Native::IAM::RolePolicyAttachment", "app/"+policy,
			map[string]string{"role": "app", "policy_arn": policy},
			tfstate.Instance{ID: "app/" + policy, Identity: global("role", "app", "policy_arn", policy)}),
		{
			tf:   "aws_iam_user_policy_attachment",
			noID: true,
			live: native(
				"",
				"Native::IAM::UserPolicyAttachment",
				"ci/"+policy,
				map[string]string{"user": "ci", "policy_arn": policy},
				tfstate.Instance{},
			).live,
			state: tfstate.Instance{
				ID:       "ci-20260101000000000000000001",
				Identity: global("user", "ci", "policy_arn", policy),
			},
		},
		{
			tf:   "aws_iam_group_policy_attachment",
			noID: true,
			live: native(
				"",
				"Native::IAM::GroupPolicyAttachment",
				"ops/"+policy,
				map[string]string{"group": "ops", "policy_arn": policy},
				tfstate.Instance{},
			).live,
			state: tfstate.Instance{
				ID:       "ops-20260101000000000000000001",
				Identity: global("group", "ops", "policy_arn", policy),
			},
		},
		native(
			"aws_iam_role_policy",
			"AWS::IAM::RolePolicy",
			"app:inline",
			map[string]string{"role": "app", "name": "inline"},
			tfstate.Instance{ID: "app:inline", Identity: global("role", "app", "name", "inline")},
		),
		native(
			"aws_iam_user_policy",
			"AWS::IAM::UserPolicy",
			"ci:inline",
			map[string]string{"user": "ci", "name": "inline"},
			tfstate.Instance{ID: "ci:inline", Identity: global("user", "ci", "name", "inline")},
		),
		native(
			"aws_iam_group_policy",
			"AWS::IAM::GroupPolicy",
			"ops:inline",
			map[string]string{"group": "ops", "name": "inline"},
			tfstate.Instance{ID: "ops:inline"},
		),
		{tf: "aws_s3_bucket", live: live("AWS::S3::Bucket", func(r *inventory.CloudResource) {
			r.ARN, r.ID = "arn:aws:s3:::acme-logs", "acme-logs"
		}), state: tfstate.Instance{ID: "acme-logs", ARN: "arn:aws:s3:::acme-logs", Region: region, Identity: regional("bucket", "acme-logs")}},
		bucketSub("aws_s3_bucket_policy", "AWS::S3::BucketPolicy"),
		bucketSub("aws_s3_bucket_versioning", "Native::S3::BucketVersioning"),
		bucketSub("aws_s3_bucket_server_side_encryption_configuration", "Native::S3::BucketEncryption"),
		bucketSub("aws_s3_bucket_lifecycle_configuration", "Native::S3::BucketLifecycle"),
		bucketSub("aws_s3_bucket_public_access_block", "Native::S3::BucketPublicAccessBlock"),
		{tf: "aws_kms_key", live: live("AWS::KMS::Key", func(r *inventory.CloudResource) {
			r.ARN, r.ID = arnOf("kms", "key/0f1e2d3c"), "0f1e2d3c"
		}), state: tfstate.Instance{ID: "0f1e2d3c", ARN: arnOf("kms", "key/0f1e2d3c"), Identity: regional("id", "0f1e2d3c")}},
		{tf: "aws_kms_alias", live: live("AWS::KMS::Alias", func(r *inventory.CloudResource) {
			r.ARN = arnOf("kms", "alias/app")
		}), state: tfstate.Instance{ID: "alias/app", ARN: arnOf("kms", "alias/app"), Identity: regional("name", "alias/app")}},
		{tf: "aws_db_instance", live: live("AWS::RDS::DBInstance", func(r *inventory.CloudResource) {
			r.ARN, r.ID = arnOf("rds", "db:orders"), "db-ABCDEFGHIJ"
		}), state: tfstate.Instance{ID: "db-ABCDEFGHIJ", ARN: arnOf("rds", "db:orders"), Identity: regional("identifier", "orders")}},
		{tf: "aws_rds_cluster", live: live("AWS::RDS::DBCluster", func(r *inventory.CloudResource) {
			r.ARN, r.ID = arnOf("rds", "cluster:orders"), "cluster-ABCDEF"
		}), state: tfstate.Instance{ID: "orders", ARN: arnOf("rds", "cluster:orders"), Identity: regional("cluster_identifier", "orders")}},
		{tf: "aws_db_subnet_group", live: live("AWS::RDS::DBSubnetGroup", func(r *inventory.CloudResource) {
			r.ARN = arnOf("rds", "subgrp:private")
		}), state: tfstate.Instance{ID: "private", ARN: arnOf("rds", "subgrp:private"), Identity: regional("name", "private")}},
		{tf: "aws_eks_cluster", live: live("AWS::EKS::Cluster", func(r *inventory.CloudResource) {
			r.ARN = arnOf("eks", "cluster/main")
		}), state: tfstate.Instance{ID: "main", ARN: arnOf("eks", "cluster/main"), Identity: regional("name", "main")}},
		{tf: "aws_eks_node_group", live: live("AWS::EKS::Nodegroup", func(r *inventory.CloudResource) {
			r.ARN, r.Sources = arnOf("eks", "nodegroup/main/default/6ac6a5c2"), []string{inventory.LayerRE}
		}), state: tfstate.Instance{
			ID: "main:default", ARN: arnOf("eks", "nodegroup/main/default/6ac6a5c2"),
			Identity: regional("cluster_name", "main", "node_group_name", "default"),
		}},
		lb("aws_lb", "AWS::ElasticLoadBalancingV2::LoadBalancer", lbARN),
		lb("aws_alb", "AWS::ElasticLoadBalancingV2::LoadBalancer", lbARN),
		lb("aws_lb_listener", "AWS::ElasticLoadBalancingV2::Listener", listenerARN),
		lb("aws_alb_listener", "AWS::ElasticLoadBalancingV2::Listener", listenerARN),
		lb("aws_lb_target_group", "AWS::ElasticLoadBalancingV2::TargetGroup", tgARN),
		lb("aws_alb_target_group", "AWS::ElasticLoadBalancingV2::TargetGroup", tgARN),
		{tf: "aws_route53_zone", live: live("AWS::Route53::HostedZone", func(r *inventory.CloudResource) {
			r.Region, r.ARN, r.ID = "", "arn:aws:route53:::hostedzone/Z1", "Z1"
		}), state: tfstate.Instance{ID: "Z1", ARN: "arn:aws:route53:::hostedzone/Z1", Identity: global("zone_id", "Z1")}},
		{tf: "aws_route53_record", live: live("AWS::Route53::RecordSet", func(r *inventory.CloudResource) {
			r.Region, r.ID, r.Sources = "", "Z1_www.example.com_A", []string{inventory.LayerRoute53Records}
			r.Attrs = map[string]string{
				"zone_id":        "Z1",
				"name":           "www.example.com",
				"type":           "A",
				"set_identifier": "",
				"short_name":     "www",
				"apex":           "false",
			}
		}), state: tfstate.Instance{ID: "Z1_www_A", Identity: global("zone_id", "Z1", "name", "www.example.com", "type", "A")}},
		{tf: "aws_lambda_function", live: live("AWS::Lambda::Function", func(r *inventory.CloudResource) {
			r.ARN, r.ID = arnOf("lambda", "function:resize"), "resize"
		}), state: tfstate.Instance{ID: "resize", ARN: arnOf("lambda", "function:resize"), Identity: regional("function_name", "resize")}},
		{tf: "aws_sqs_queue", live: live("AWS::SQS::Queue", func(r *inventory.CloudResource) {
			r.ARN, r.ID = arnOf("sqs", "jobs"), queueURL
		}), state: tfstate.Instance{ID: queueURL, ARN: arnOf("sqs", "jobs"), Identity: ident("url", queueURL)}},
		{tf: "aws_sns_topic", live: live("AWS::SNS::Topic", func(r *inventory.CloudResource) {
			r.ARN = arnOf("sns", "alerts")
		}), state: tfstate.Instance{ID: arnOf("sns", "alerts"), ARN: arnOf("sns", "alerts"), Identity: ident("arn", arnOf("sns", "alerts"))}},
		{tf: "aws_ecr_repository", live: live("AWS::ECR::Repository", func(r *inventory.CloudResource) {
			r.ARN = arnOf("ecr", "repository/team/app")
		}), state: tfstate.Instance{ID: "team/app", ARN: arnOf("ecr", "repository/team/app"), Identity: regional("name", "team/app")}},
		{tf: "aws_secretsmanager_secret", live: live("AWS::SecretsManager::Secret", func(r *inventory.CloudResource) {
			r.ARN = arnOf("secretsmanager", "secret:db-AbCdEf")
		}), state: tfstate.Instance{
			ID: arnOf("secretsmanager", "secret:db-AbCdEf"), ARN: arnOf("secretsmanager", "secret:db-AbCdEf"),
			Identity: ident("arn", arnOf("secretsmanager", "secret:db-AbCdEf")),
		}},
		{tf: "aws_cloudwatch_log_group", live: live("AWS::Logs::LogGroup", func(r *inventory.CloudResource) {
			r.ARN, r.Sources = arnOf("logs", "log-group:/aws/lambda/resize:*"), []string{inventory.LayerRE}
		}), state: tfstate.Instance{ID: "/aws/lambda/resize", ARN: arnOf("logs", "log-group:/aws/lambda/resize"), Identity: regional("name", "/aws/lambda/resize")}},
		{tf: "aws_dynamodb_table", live: live("AWS::DynamoDB::Table", func(r *inventory.CloudResource) {
			r.ARN, r.ID = arnOf("dynamodb", "table/orders"), "orders"
		}), state: tfstate.Instance{ID: "orders", ARN: arnOf("dynamodb", "table/orders"), Identity: regional("name", "orders")}},
	}
}

// TestTypes matches every curated type in four forms of its state: with identity, with identity
// read from attributes (states before Terraform 1.12), by ARN, and by ID. Each form must find
// the same address, which also shows identity and ARN matching agree.
func TestTypes(t *testing.T) {
	t.Parallel()

	m := loadMap(t)
	covered := map[string]bool{}
	for _, tc := range typeCases() {
		covered[tc.tf] = true
		ty, ok := m.Type(tc.tf)
		require.True(t, ok, tc.tf)
		variants := []struct {
			name string
			inst tfstate.Instance
			want tfstate.KeyKind
			skip bool
		}{
			{"identity", tc.state, tfstate.KeyIdentity, tc.state.Identity == nil},
			{"identity from attributes", attrsOnly(tc.state), attrsKey(tc.state), tc.state.Identity == nil},
			{
				"arn",
				tfstate.Instance{ID: tc.state.ID, ARN: tc.state.ARN, Region: tc.state.Region},
				tfstate.KeyARN,
				tc.state.ARN == "",
			},
			{
				"id",
				tfstate.Instance{ID: tc.state.ID, Region: tc.state.Region},
				tfstate.KeyID,
				tc.noID || len(ty.ID) == 0,
			},
		}
		for _, v := range variants {
			if v.skip {
				continue
			}
			ix := tfstate.BuildIndex([]tfstate.UnitState{unitState("app", tc.tf, v.inst)})
			got := match.New(m, ix).Match([]inventory.CloudResource{tc.live})[0]
			assert.Equal(t, match.CategoryManaged, got.Category, "%s by %s: %s", tc.tf, v.name, got.Reason)
			assert.Equal(t, v.want, got.Key, "%s by %s", tc.tf, v.name)
			if assert.Len(t, got.ManagedBy, 1, "%s by %s", tc.tf, v.name) {
				assert.Equal(t, tc.tf+".this", got.ManagedBy[0].Address)
			}
			assert.NotEmpty(t, got.Rule)
		}
	}
	for _, ty := range m.Curated() {
		if ty.TF != "aws_security_group_rule" {
			assert.True(t, covered[ty.TF], "curated type %s has no test case", ty.TF)
		}
	}
	assert.GreaterOrEqual(t, len(covered), 40)
}

// attrsKey is the key an identity read from attributes matches by: the identity, unless the
// state's ARN has no account (S3 buckets, hosted zones) and the identity can't be completed.
func attrsKey(inst tfstate.Instance) tfstate.KeyKind {
	if a, err := arn.Parse(inst.ARN); err == nil && a.AccountID == "" {
		return tfstate.KeyARN
	}
	return tfstate.KeyIdentity
}

// attrsOnly moves the identity's own attributes to IdentityAttrs, as the parser fills them from
// resource attributes when state has no identity block.
func attrsOnly(inst tfstate.Instance) tfstate.Instance {
	out := tfstate.Instance{ID: inst.ID, ARN: inst.ARN, Region: inst.Region}
	if inst.Identity == nil {
		return out
	}
	out.IdentityAttrs = maps.Clone(inst.Identity)
	delete(out.IdentityAttrs, "account_id")
	delete(out.IdentityAttrs, "region")
	if len(out.IdentityAttrs) == 0 {
		out.IdentityAttrs = nil
	}
	return out
}

func TestNoFalseMatch(t *testing.T) {
	t.Parallel()

	m := loadMap(t)
	ix := tfstate.BuildIndex([]tfstate.UnitState{
		unitState("net", "aws_vpc", tfstate.Instance{ID: "vpc-1", ARN: arnOf("ec2", "vpc/vpc-1")}),
		// An aws_sns_topic_policy's arn is its topic's ARN; it must not claim the topic.
		unitState(
			"msg",
			"aws_sns_topic_policy",
			tfstate.Instance{ID: arnOf("sns", "alerts"), ARN: arnOf("sns", "alerts")},
		),
	})
	got := match.New(m, ix).Match([]inventory.CloudResource{
		live("AWS::EC2::VPC", func(r *inventory.CloudResource) { r.ARN, r.ID = arnOf("ec2", "vpc/vpc-2"), "vpc-2" }),
		live("AWS::EC2::VPC", func(r *inventory.CloudResource) {
			r.Account, r.ARN, r.ID = "222222222222", "arn:aws:ec2:eu-west-1:222222222222:vpc/vpc-1", "vpc-1"
		}),
		live("AWS::SNS::Topic", func(r *inventory.CloudResource) { r.ARN = arnOf("sns", "alerts") }),
	})
	for _, r := range got {
		assert.Equal(t, match.CategoryUnmatched, r.Category, r.Resource.ARN)
		assert.Equal(t, match.RuleNotInState, r.Rule)
	}
}

func TestUndetermined(t *testing.T) {
	t.Parallel()

	m := loadMap(t)
	ix := tfstate.BuildIndex([]tfstate.UnitState{
		unitState("etl", "aws_glue_job", tfstate.Instance{ID: "nightly", ARN: arnOf("glue", "job/nightly")}),
	})
	got := match.New(m, ix).Match([]inventory.CloudResource{
		live("AWS::Glue::Job", func(r *inventory.CloudResource) { r.ARN = arnOf("glue", "job/nightly") }),
		live("", func(r *inventory.CloudResource) {
			r.SourceType, r.ARN = "quicksight:dashboard", arnOf("quicksight", "dashboard/d")
		}),
	})
	for _, r := range got {
		assert.Equal(t, match.CategoryUndetermined, r.Category, "an unmapped type is never managed")
		assert.Equal(t, match.RuleNoMapping, r.Rule)
	}
	assert.Equal(t, "no type mapping for AWS::Glue::Job", got[0].Reason)
	assert.Equal(t, "no type mapping for quicksight:dashboard", got[1].Reason)
	s := match.Summarize(got)
	assert.Equal(t, 2, s.Counts[match.CategoryUndetermined])
	assert.Equal(t, 1, s.Undetermined["no type mapping for AWS::Glue::Job"])
}

func TestDoubleManaged(t *testing.T) {
	t.Parallel()

	m := loadMap(t)
	role := tfstate.Instance{ID: "app", ARN: iamARN("role/app")}
	ix := tfstate.BuildIndex(
		[]tfstate.UnitState{unitState("old", "aws_iam_role", role), unitState("new", "aws_iam_role", role)},
	)
	got := match.New(m, ix).Match([]inventory.CloudResource{
		live("AWS::IAM::Role", func(r *inventory.CloudResource) { r.Region, r.ARN = "", iamARN("role/app") }),
	})[0]
	assert.Equal(t, match.CategoryDoubleManaged, got.Category)
	assert.Equal(t, match.RuleDoubleManage, got.Rule)
	assert.Len(t, got.ManagedBy, 2)
	assert.Equal(t, "live/new", got.ManagedBy[0].Unit)
}

func TestDerived(t *testing.T) {
	t.Parallel()

	m := loadMap(t)
	ix := tfstate.BuildIndex([]tfstate.UnitState{
		unitState("dns", "aws_route53_zone", tfstate.Instance{ID: "Z1", ARN: "arn:aws:route53:::hostedzone/Z1"}),
		unitState(
			"data",
			"aws_s3_bucket",
			tfstate.Instance{ID: "acme-logs", ARN: "arn:aws:s3:::acme-logs", Region: region},
		),
	})
	record := func(name, typ, apex string) inventory.CloudResource {
		return live("AWS::Route53::RecordSet", func(r *inventory.CloudResource) {
			r.Region, r.ID = "", "Z1_"+name+"_"+typ
			r.Attrs = map[string]string{"zone_id": "Z1", "name": name, "type": typ, "apex": apex}
			r.Parents = []inventory.Ref{
				{CFNType: "AWS::Route53::HostedZone", ID: "Z1", ARN: "arn:aws:route53:::hostedzone/Z1"},
			}
		})
	}
	sub := func(cfn string) inventory.CloudResource {
		return live(cfn, func(r *inventory.CloudResource) {
			r.ID, r.Attrs = "acme-logs", map[string]string{"bucket": "acme-logs"}
			r.Parents = []inventory.Ref{{CFNType: "AWS::S3::Bucket", ID: "acme-logs", ARN: "arn:aws:s3:::acme-logs"}}
		})
	}
	got := match.New(m, ix).Match([]inventory.CloudResource{
		record("example.com", "NS", "true"),
		record("example.com", "SOA", "true"),
		record("www.example.com", "A", "false"),
		sub("Native::S3::BucketEncryption"),
		sub("Native::S3::BucketPublicAccessBlock"),
		sub("AWS::S3::BucketPolicy"),
	})
	want := []match.Category{
		match.CategoryDerived, match.CategoryDerived, match.CategoryUnmatched,
		match.CategoryDerived, match.CategoryDerived, match.CategoryUnmatched,
	}
	for i, r := range got {
		assert.Equal(t, want[i], r.Category, "%s %s: %s", r.Resource.CFNType, r.Resource.ID, r.Reason)
	}
	assert.Equal(t, "child of managed aws_route53_zone.this (AWS::Route53::HostedZone)", got[0].Reason)
	assert.Equal(t, match.RuleParent, got[0].Rule)
}

// TestSecurityGroupRules matches rules declared three ways: the per-rule resources (by id), the
// legacy aws_security_group_rule (by tuple, one TF resource per several AWS rules) and inline
// blocks of aws_security_group (by tuple, only when no rule resource has the tuple).
func TestSecurityGroupRules(t *testing.T) {
	t.Parallel()

	m := loadMap(t)
	web := []tfstate.SGRule{
		{GroupID: "sg-web", Protocol: "tcp", FromPort: 443, ToPort: 443, Source: "cidr4:10.0.0.0/8"},
		{GroupID: "sg-web", Egress: true, Protocol: "-1", FromPort: -1, ToPort: -1, Source: "cidr4:0.0.0.0/0"},
	}
	legacy := []tfstate.SGRule{
		{GroupID: "sg-db", Protocol: "tcp", FromPort: 5432, ToPort: 5432, Source: "cidr4:10.1.0.0/16"},
		{GroupID: "sg-db", Protocol: "tcp", FromPort: 5432, ToPort: 5432, Source: "cidr4:10.2.0.0/16"},
	}
	keys := func(rules ...tfstate.SGRule) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			out = append(out, r.Key())
		}
		return out
	}
	newRuleARN := arnOf("ec2", "security-group-rule/sgr-new")
	ix := tfstate.BuildIndex([]tfstate.UnitState{
		// The SG resource refreshes every rule into its inline blocks, the legacy ones included.
		unitState("web", "aws_security_group", tfstate.Instance{
			ID: "sg-web", ARN: arnOf("ec2", "security-group/sg-web"), RuleKeys: keys(append(web, legacy...)...),
		}),
		unitState(
			"db",
			"aws_security_group_rule",
			tfstate.Instance{ID: "sgrule-2961913876", RuleKeys: keys(legacy...)},
		),
		unitState("api", "aws_vpc_security_group_ingress_rule", tfstate.Instance{ID: "sgr-new", ARN: newRuleARN}),
	})
	rule := func(id string, r tfstate.SGRule, cfn string) inventory.CloudResource {
		return live(cfn, func(c *inventory.CloudResource) {
			c.ARN, c.ID = arnOf("ec2", "security-group-rule/"+id), id
			c.Attrs = map[string]string{"group_id": r.GroupID, "rule_key": r.Key()}
		})
	}
	newRule := tfstate.SGRule{GroupID: "sg-api", Protocol: "tcp", FromPort: 80, ToPort: 80, Source: "sg:sg-web"}
	got := match.New(m, ix).Match([]inventory.CloudResource{
		rule("sgr-a", web[0], "AWS::EC2::SecurityGroupIngress"),
		rule("sgr-b", web[1], "AWS::EC2::SecurityGroupEgress"),
		rule("sgr-c", legacy[0], "AWS::EC2::SecurityGroupIngress"),
		rule("sgr-d", legacy[1], "AWS::EC2::SecurityGroupIngress"),
		rule("sgr-new", newRule, "AWS::EC2::SecurityGroupIngress"),
		rule(
			"sgr-e",
			tfstate.SGRule{GroupID: "sg-web", Protocol: "tcp", FromPort: 22, ToPort: 22, Source: "cidr4:0.0.0.0/0"},
			"AWS::EC2::SecurityGroupIngress",
		),
	})
	want := []struct {
		category Category
		rule     string
		address  string
	}{
		{match.CategoryManaged, match.RuleInline, "aws_security_group.this"},
		{match.CategoryManaged, match.RuleInline, "aws_security_group.this"},
		{match.CategoryManaged, match.RuleTuple, "aws_security_group_rule.this"},
		{match.CategoryManaged, match.RuleTuple, "aws_security_group_rule.this"},
		{match.CategoryManaged, match.RuleARN, "aws_vpc_security_group_ingress_rule.this"},
		{match.CategoryUnmatched, match.RuleNotInState, ""},
	}
	for i, w := range want {
		assert.Equal(t, w.category, got[i].Category, "rule %d: %s", i, got[i].Reason)
		assert.Equal(t, w.rule, got[i].Rule, "rule %d", i)
		if w.address != "" && assert.NotEmpty(t, got[i].ManagedBy, "rule %d", i) {
			assert.Equal(t, w.address, got[i].ManagedBy[0].Address, "rule %d", i)
		}
	}
}

// Category is match.Category, shortened for the table above.
type Category = match.Category

func TestIdentityParseAttrs(t *testing.T) {
	t.Parallel()

	f := match.IdentityParseAttrs(loadMap(t))
	assert.Equal(
		t,
		map[string]string{"zone_id": "zone_id", "name": "fqdn", "type": "type", "set_identifier": "set_identifier"},
		f("aws_route53_record"),
	)
	assert.Equal(
		t,
		map[string]string{"group": "group", "policy_arn": "policy_arn"},
		f("aws_iam_group_policy_attachment"),
	)
	assert.Nil(t, f("aws_glue_job"))
}
