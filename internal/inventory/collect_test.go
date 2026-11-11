package inventory_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

const goldenDir = "../../testdata/inventory"

// twoRegions is a probe result where both regions have a Resource Explorer default view and
// only eu-west-1 records Config (with global types), like a Control Tower member account
// without a recorder in us-east-1.
func twoRegions() *probe.Report {
	re := probe.RegionRE{Status: probe.StatusAvailable, Index: probe.IndexLocal, Views: 1, DefaultView: true}
	return &probe.Report{Account: acct, Regions: []probe.Region{
		{
			Region: usEast, ResourceExplorer: re,
			Config: probe.ConfigCheck{Status: probe.StatusUnavailable, Recorders: []probe.Recorder{}},
		},
		{
			Region: euWest, ResourceExplorer: re,
			Config: probe.ConfigCheck{Status: probe.StatusAvailable, Recorders: []probe.Recorder{
				{
					Name:          "aws-controltower-BaselineConfigRecorder",
					Recording:     true,
					AllSupported:  true,
					IncludeGlobal: true,
					Frequency:     "CONTINUOUS",
				},
			}},
		},
	}}
}

// fleetStubs serves both regions. The security group sg-1 is reported by Config (tags Name=web)
// and by Resource Explorer (tags team=net), and its rule by the native lister.
func fleetStubs(r *awstest.Router) {
	sg := "arn:aws:ec2:eu-west-1:" + acct + ":security-group/sg-1"
	role := "arn:aws:iam::" + acct + ":role/app"
	r.On("config", "SelectResourceConfig", "", ok(configPage("",
		configRow("AWS::EC2::SecurityGroup", sg, "sg-1", euWest, "Name", "web"),
		configRow("AWS::IAM::Role", role, "AROAEXAMPLE", "global"))))
	r.On("resource-explorer-2", "ListResources", "region:eu-west-1", ok(rePage("",
		reResource(sg, "ec2:security-group", euWest, "team", "net"))))
	r.On("resource-explorer-2", "ListResources", "region:us-east-1", ok(rePage("",
		reResource(role, "iam:role", "global"),
		reResource("arn:aws:ec2:us-east-1:"+acct+":vpc/vpc-9", "ec2:vpc", usEast))))
	r.On("ec2", "DescribeSecurityGroupRules", "ec2.eu-west-1.", ok(sgPage("",
		sgRule("sgr-1", false, "tcp", 443, 443, "10.0.0.0/8"))))
	r.On("ec2", "DescribeSecurityGroupRules", "ec2.us-east-1.", ok(sgPage("")))
	r.On("s3", "ListBuckets", "bucket-region=eu-west-1", ok(buckets("", "acme-logs")))
	r.On("s3", "ListBuckets", "bucket-region=us-east-1", ok(buckets("")))
	bucketConfig(r, "acme-logs")
	r.On("iam", "GetAccountAuthorizationDetails", "", ok(iamPage("", iamRole("app"))))
	r.On("route53", "ListHostedZones", "", ok(zones()))
	r.On("route53", "ListResourceRecordSets", "", ok(records("", recordSet("example.com.", "NS", ""))))
}

func collect(t *testing.T, rt http.RoundTripper, concurrency int) *inventory.Result {
	t.Helper()
	e := env(t)
	res, err := inventory.NewCollector(inventory.Options{Env: e, Concurrency: concurrency}).
		Collect(t.Context(), inventory.Request{
			Targets: []inventory.Target{{Account: account(rt), Report: twoRegions()}},
		})
	require.NoError(t, err)
	return res
}

func TestCollect(t *testing.T) {
	t.Parallel()

	res := collect(t, transport(t, "inventory-two-regions", fleetStubs), 16)

	var sg *inventory.CloudResource
	for i := range res.Resources {
		if res.Resources[i].ID == "sg-1" {
			sg = &res.Resources[i]
		}
	}
	require.NotNil(t, sg, "the security group is missing")
	assert.Equal(t, map[string]string{"Name": "web"}, sg.Tags, "tags come from config")
	assert.Equal(t, []string{inventory.LayerConfig, inventory.LayerRE}, sg.Sources)
	assert.Equal(t, []string{"aws_security_group"}, sg.TFTypes)

	var cov bytes.Buffer
	require.NoError(t, inventory.WriteCoverage(&cov, res.Coverage))
	testutil.Golden(t, filepath.Join(goldenDir, "coverage-two-regions.golden"), cov.Bytes())

	var lines []string
	for i := range res.Resources {
		lines = append(lines, describe(&res.Resources[i])+" "+res.Resources[i].Region)
	}
	runs, err := json.MarshalIndent(map[string]any{"resources": lines, "runs": res.Runs}, "", "  ")
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join(goldenDir, "collect-two-regions.golden.json"), append(runs, '\n'))
	for _, run := range res.Runs {
		if run.Layer == inventory.LayerConfig && run.Region == usEast {
			assert.Equal(t, inventory.RunUnsupported, run.Status)
		} else {
			assert.Equal(t, inventory.RunOK, run.Status, "%s %s", run.Layer, run.Region)
		}
	}
}

// TestCollectDeterministic runs the same listing with every task in parallel and with one at a
// time: the result must not depend on which listing finishes first.
func TestCollectDeterministic(t *testing.T) {
	t.Parallel()

	if testutil.Updating() {
		t.Skip("TestCollect writes the cassette")
	}
	serial := collect(t, awstest.Replay(t, filepath.Join(cassettes, "inventory-two-regions")), 1)
	parallel := collect(t, awstest.Replay(t, filepath.Join(cassettes, "inventory-two-regions")), 16)
	a, err := json.Marshal(serial)
	require.NoError(t, err)
	b, err := json.Marshal(parallel)
	require.NoError(t, err)
	assert.JSONEq(t, string(a), string(b))
}

func TestMerge(t *testing.T) {
	t.Parallel()

	sg := "arn:aws:ec2:eu-west-1:" + acct + ":security-group/sg-1"
	native := inventory.CloudResource{
		Account: acct, Region: euWest, CFNType: "AWS::EC2::SecurityGroupIngress", ID: "sgr-1",
		ARN: "arn:aws:ec2:eu-west-1:" + acct + ":security-group-rule/sgr-1", Sources: []string{inventory.LayerSGRules},
		Parents: []inventory.Ref{{CFNType: "AWS::EC2::SecurityGroup", ID: "sg-1", ARN: sg}},
	}
	re := inventory.CloudResource{
		Account: acct, Region: euWest, CFNType: "AWS::EC2::SecurityGroup", ARN: sg,
		Tags: map[string]string{"team": "net"}, Sources: []string{inventory.LayerRE},
	}
	cfg := inventory.CloudResource{
		Account: acct, Region: euWest, CFNType: "AWS::EC2::SecurityGroup", ARN: sg, ID: "sg-1",
		Name: "web", Tags: map[string]string{"Name": "web"}, Sources: []string{inventory.LayerConfig},
	}
	// An unknown type from Resource Explorer gets its type from a later layer.
	unknown := inventory.CloudResource{
		Account: acct, Region: euWest, ARN: "arn:aws:x:eu-west-1:" + acct + ":y/z",
		SourceType: "x:y", Sources: []string{inventory.LayerRE},
	}

	for _, order := range [][]inventory.CloudResource{{native, re, cfg, unknown}, {cfg, unknown, native, re}} {
		got := inventory.Merge(order, env(t))
		require.Len(t, got, 3)
		byARN := map[string]inventory.CloudResource{}
		for _, r := range got {
			byARN[r.ARN] = r
		}
		merged := byARN[sg]
		assert.Equal(t, "AWS::EC2::SecurityGroup", merged.CFNType)
		assert.Equal(t, "sg-1", merged.ID)
		assert.Equal(t, map[string]string{"Name": "web"}, merged.Tags)
		assert.Equal(t, []string{inventory.LayerConfig, inventory.LayerRE}, merged.Sources)
		assert.Equal(
			t,
			[]string{"aws_security_group_rule", "aws_vpc_security_group_ingress_rule"},
			byARN[native.ARN].TFTypes,
		)
		assert.Equal(t, "x:y", byARN[unknown.ARN].SourceType)
		assert.Equal(t, []string{"", "AWS::EC2::SecurityGroup", "AWS::EC2::SecurityGroupIngress"},
			[]string{got[0].CFNType, got[1].CFNType, got[2].CFNType}, "sorted by account, region, type")
	}
}

func TestSelectLayers(t *testing.T) {
	t.Parallel()

	c := inventory.NewCollector(inventory.Options{Env: env(t)})
	_, err := c.Collect(t.Context(), inventory.Request{Layers: []string{"cloudcontrol"}})
	require.ErrorIs(t, err, inventory.ErrUnknownLayer)

	res, err := c.Collect(t.Context(), inventory.Request{Layers: []string{"native", "config"}})
	require.NoError(t, err)
	assert.Empty(t, res.Resources)
}

func TestLimiters(t *testing.T) {
	t.Parallel()

	l := inventory.NewLimiters(map[string]rate.Limit{inventory.ServiceEC2: 3})
	ec2 := l.For(acct, euWest, inventory.ServiceEC2)
	assert.Same(t, ec2, l.For(acct, euWest, inventory.ServiceEC2))
	assert.NotSame(t, l.For(acct, euWest, inventory.ServiceEC2), l.For(acct, usEast, inventory.ServiceEC2))
	assert.NotSame(t, l.For(acct, euWest, inventory.ServiceEC2), l.For("222222222222", euWest, inventory.ServiceEC2))
	assert.NotSame(t, l.For(acct, euWest, inventory.ServiceEC2), l.For(acct, euWest, inventory.ServiceS3))
	// IAM and Route 53 are global: one budget per account.
	assert.Same(t, l.For(acct, euWest, inventory.ServiceIAM), l.For(acct, usEast, inventory.ServiceIAM))
	assert.InDelta(t, 3, float64(ec2.Limit()), 0)
	assert.InDelta(t, 5, float64(l.For(acct, euWest, inventory.ServiceRE).Limit()), 0)
	assert.Equal(t, 5, l.For(acct, euWest, inventory.ServiceRoute53).Burst())
}

func TestCapabilities(t *testing.T) {
	t.Parallel()

	r := twoRegions()
	assert.Equal(t, inventory.Capabilities{ResourceExplorer: true}, inventory.CapabilitiesFrom(&r.Regions[0]))
	assert.Equal(
		t,
		inventory.Capabilities{Config: true, ConfigGlobal: true, ResourceExplorer: true},
		inventory.CapabilitiesFrom(&r.Regions[1]),
	)
	r.Regions[0].ResourceExplorer.DefaultView = false
	assert.False(
		t,
		inventory.CapabilitiesFrom(&r.Regions[0]).ResourceExplorer,
		"a view that isn't the default is never picked",
	)
	assert.Equal(t, "cn-north-1", inventory.GlobalRegion("aws-cn"))
	assert.Equal(t, "us-gov-west-1", inventory.GlobalRegion("aws-us-gov"))
	assert.Equal(t, "us-east-1", inventory.GlobalRegion("aws"))
}
