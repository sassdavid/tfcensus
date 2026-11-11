//go:build emulator

package inventory_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	retypes "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/inventory"
)

// The nightly live workflow sets these to the emulators it starts; without them the test skips.
const (
	envMotoURL  = "TFCENSUS_MOTO_URL"  // motoserver/moto
	envFlociURL = "TFCENSUS_FLOCI_URL" // floci/floci
)

// emulatorAccount returns an account in region whose clients all go to endpoint, with the
// account ID the emulator reports.
func emulatorAccount(t *testing.T, endpoint, region string) *accounts.Account {
	t.Helper()
	cfg := aws.Config{
		Region:       region,
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	}
	id, err := sts.NewFromConfig(cfg).GetCallerIdentity(t.Context(), &sts.GetCallerIdentityInput{})
	require.NoError(t, err)
	return &accounts.Account{ID: aws.ToString(id.Account), Partition: "aws", Config: cfg}
}

func emulatorEnv(t *testing.T) *inventory.Env {
	t.Helper()
	e := env(t)
	e.S3PathStyle = true
	e.Limits = inventory.NewLimiters(nil)
	return e
}

// found lists every resource the layers report, as "type id-or-arn" lines.
func found(t *testing.T, acct *accounts.Account, layers []inventory.Layer) []string {
	t.Helper()
	var out []string
	for _, l := range layers {
		region := acct.Config.Region
		if l.Global() {
			region = ""
		}
		for r, err := range l.List(t.Context(), acct, region, nil) {
			require.NoError(t, err, l.Name())
			out = append(out, r.CFNType+" "+r.ARN+" "+r.ID+" "+strings.Join(r.Sources, ","))
		}
	}
	return out
}

func requireFound(t *testing.T, lines []string, what string, parts ...string) {
	t.Helper()
	ok := slices.ContainsFunc(lines, func(l string) bool {
		for _, p := range parts {
			if !strings.Contains(l, p) {
				return false
			}
		}
		return true
	})
	require.True(t, ok, "no layer found %s (want %q) in:\n%s", what, parts, strings.Join(lines, "\n"))
}

// TestSpec_inventory_5: ClickOps resources created with the SDK in moto (EC2, IAM, Route 53, S3)
// and Floci (Resource Explorer, Cloud Control) are each found by at least one layer. Config
// Select* is covered by the cassette tests, since no emulator runs Config SQL.
func TestSpec_inventory_5(t *testing.T) {
	moto, floci := os.Getenv(envMotoURL), os.Getenv(envFlociURL)
	if moto == "" || floci == "" {
		t.Skipf("set %s and %s to the emulators", envMotoURL, envFlociURL)
	}
	t.Run("moto", func(t *testing.T) {
		acct := emulatorAccount(t, moto, "us-east-1")
		clickOps(t, acct.Config)
		lines := found(t, acct, inventory.NativeLayers(emulatorEnv(t)))
		requireFound(t, lines, "the security group rule", "AWS::EC2::SecurityGroupIngress", inventory.LayerSGRules)
		requireFound(
			t,
			lines,
			"the role's managed policy attachment",
			"Native::IAM::RolePolicyAttachment",
			"clickops-role",
		)
		requireFound(t, lines, "the role's inline policy", "AWS::IAM::RolePolicy", "clickops-inline")
		requireFound(t, lines, "the hosted zone's records", "AWS::Route53::RecordSet", "_clickops.test_NS")
		requireFound(t, lines, "the bucket's versioning", "Native::S3::BucketVersioning", "tfcensus-clickops")
	})
	// Floci 2.4.0 combines repeated region: filters with AND where AWS uses OR, so the
	// us-east-1 filter (region:us-east-1 region:global) finds nothing there. eu-west-1 sends
	// one region term.
	t.Run("floci", func(t *testing.T) {
		acct := emulatorAccount(t, floci, "eu-west-1")
		vpc, _ := clickOpsNetwork(t, acct.Config)
		clickOpsBucket(t, acct.Config)
		resourceExplorer(t, acct.Config)
		e := emulatorEnv(t)
		e.CloudControlTypes = []string{"AWS::EC2::VPC", "AWS::S3::Bucket"}
		lines := found(t, acct, []inventory.Layer{inventory.NewResourceExplorer(e), inventory.NewCloudControl(e)})
		requireFound(t, lines, "the bucket through Resource Explorer", "tfcensus-clickops", inventory.LayerRE)
		requireFound(t, lines, "the VPC through Cloud Control", vpc, inventory.LayerCC)
		requireFound(t, lines, "the bucket through Cloud Control", "tfcensus-clickops", inventory.LayerCC)
	})
}

// clickOps creates resources the way someone clicking in the console would: a VPC with a
// security group and a rule, a role with policies, a hosted zone and a versioned bucket.
func clickOps(t *testing.T, cfg aws.Config) {
	t.Helper()
	ctx := t.Context()
	clickOpsNetwork(t, cfg)
	ic := iam.NewFromConfig(cfg)
	_, err := ic.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName: aws.String("clickops-role"),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
			`"Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`),
	})
	require.NoError(t, err)
	pol, err := ic.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName: aws.String("clickops-policy"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
			`"Action":"s3:ListAllMyBuckets","Resource":"*"}]}`),
	})
	require.NoError(t, err)
	_, err = ic.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String("clickops-role"), PolicyArn: pol.Policy.Arn,
	})
	require.NoError(t, err)
	_, err = ic.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName: aws.String("clickops-role"), PolicyName: aws.String("clickops-inline"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
			`"Action":"sqs:ListQueues","Resource":"*"}]}`),
	})
	require.NoError(t, err)
	_, err = route53.NewFromConfig(cfg).CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name: aws.String(
			"clickops.test.",
		),
		CallerReference: aws.String("tfcensus-" + time.Now().Format(time.RFC3339Nano)),
	})
	require.NoError(t, err)
	clickOpsBucket(t, cfg)
	_, err = s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true }).PutBucketVersioning(ctx,
		&s3.PutBucketVersioningInput{
			Bucket:                  aws.String("tfcensus-clickops"),
			VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
		})
	require.NoError(t, err)
}

// clickOpsNetwork creates a VPC and a security group with one ingress rule, and returns their IDs.
func clickOpsNetwork(t *testing.T, cfg aws.Config) (string, string) {
	t.Helper()
	ctx := t.Context()
	ec := ec2.NewFromConfig(cfg)
	vpc, err := ec.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.99.0.0/16")})
	require.NoError(t, err)
	sg, err := ec.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: aws.String("clickops-sg"), Description: aws.String("created by hand"), VpcId: vpc.Vpc.VpcId,
	})
	require.NoError(t, err)
	_, err = ec.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: sg.GroupId,
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"), FromPort: aws.Int32(443), ToPort: aws.Int32(443),
			IpRanges: []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
		}},
	})
	require.NoError(t, err)
	return aws.ToString(vpc.Vpc.VpcId), aws.ToString(sg.GroupId)
}

func clickOpsBucket(t *testing.T, cfg aws.Config) {
	t.Helper()
	in := &s3.CreateBucketInput{Bucket: aws.String("tfcensus-clickops")}
	if cfg.Region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(cfg.Region),
		}
	}
	_, err := s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true }).CreateBucket(t.Context(), in)
	require.NoError(t, err)
}

// resourceExplorer makes sure a local index and a default view exist, which tfcensus itself never
// creates. An emulator may start with an index already.
func resourceExplorer(t *testing.T, cfg aws.Config) {
	t.Helper()
	ctx := t.Context()
	rc := resourceexplorer2.NewFromConfig(cfg)
	if _, err := rc.CreateIndex(ctx, &resourceexplorer2.CreateIndexInput{}); err != nil {
		_, ok := errors.AsType[*retypes.ConflictException](err)
		require.True(t, ok, "create index: %v", err)
	}
	waitIndex(ctx, t, rc)
	if dv, err := rc.GetDefaultView(ctx, &resourceexplorer2.GetDefaultViewInput{}); err == nil && dv.ViewArn != nil {
		return
	}
	view, err := rc.CreateView(ctx, &resourceexplorer2.CreateViewInput{
		ViewName: aws.String(
			"all-resources",
		),
		IncludedProperties: []retypes.IncludedProperty{{Name: aws.String("tags")}},
	})
	require.NoError(t, err)
	_, err = rc.AssociateDefaultView(ctx, &resourceexplorer2.AssociateDefaultViewInput{ViewArn: view.View.ViewArn})
	require.NoError(t, err)
}

func waitIndex(ctx context.Context, t *testing.T, rc *resourceexplorer2.Client) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		out, err := rc.GetIndex(ctx, &resourceexplorer2.GetIndexInput{})
		require.NoError(t, err)
		if out.State == retypes.IndexStateActive {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("the Resource Explorer index never became active")
}
