package inventory_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
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

// TestSpec_inventory_3: the coverage matrix golden for a two-region stub where only one region
// records Config. It also checks the merged collection and the run statuses.
func TestSpec_inventory_3(t *testing.T) {
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
		t.Skip("TestSpec_inventory_3 writes the cassette")
	}
	serial := collect(t, awstest.Replay(t, filepath.Join(cassettes, "inventory-two-regions")), 1)
	parallel := collect(t, awstest.Replay(t, filepath.Join(cassettes, "inventory-two-regions")), 16)
	a, err := json.Marshal(serial)
	require.NoError(t, err)
	b, err := json.Marshal(parallel)
	require.NoError(t, err)
	assert.JSONEq(t, string(a), string(b))
}

// TestSpec_inventory_2: the same security group from config, Resource Explorer and the native
// rule lister merges into one resource with config's tags and the sources config and
// resource-explorer, in either input order.
func TestSpec_inventory_2(t *testing.T) {
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
	_, err := c.Collect(t.Context(), inventory.Request{Layers: []string{"drift"}})
	require.ErrorIs(t, err, inventory.ErrUnknownLayer)

	for _, layers := range [][]string{{"native", "config"}, {"re", "cc"}, {"resource-explorer", "cloudcontrol"}} {
		res, cerr := c.Collect(t.Context(), inventory.Request{Layers: layers})
		require.NoError(t, cerr, layers)
		assert.Empty(t, res.Resources)
	}
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

const (
	cfnLB           = "AWS::ElasticLoadBalancingV2::LoadBalancer"
	cfnListener     = "AWS::ElasticLoadBalancingV2::Listener"
	cfnListenerRule = "AWS::ElasticLoadBalancingV2::ListenerRule"
)

func elbARN(resource string) string {
	return "arn:aws:elasticloadbalancing:eu-west-1:" + acct + ":" + resource
}

// parentStubs serve one load balancer from Config, its listener from Cloud Control per load
// balancer, and the listener's rule from Cloud Control per listener.
func parentStubs(r *awstest.Router) {
	lb := elbARN("loadbalancer/app/web/50dc6c495c0c9188")
	listener := elbARN("listener/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2")
	rule := elbARN("listener-rule/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2/9683b2d02a6cabee")
	r.On("config", "SelectResourceConfig", "", ok(configPage("", configRow(cfnLB, lb, lb, euWest))))
	r.On("cloudcontrolapi", "ListResources", `LoadBalancerArn\":\"`+lb, ok(ccPage("", listener)))
	r.On("cloudcontrolapi", "ListResources", `ListenerArn\":\"`+listener, ok(ccPage("", rule)))
}

// TestSpec_inventory_4: ELBv2 listeners are listed through Cloud Control with the load balancers
// the config layer found, and listener rules with the listeners Cloud Control found.
func TestSpec_inventory_4(t *testing.T) {
	t.Parallel()

	e := env(t)
	e.CloudControlTypes = []string{cfnListenerRule, cfnListener}
	rt := transport(t, "inventory-cc-parents", parentStubs)
	res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: []inventory.Layer{
		inventory.NewConfig(e), inventory.NewCloudControl(e),
	}}).Collect(t.Context(), inventory.Request{
		Targets: []inventory.Target{{Account: account(rt), Report: oneRegion()}},
		Layers:  []string{"config", "cc"},
		Types:   []string{cfnLB, cfnListener, cfnListenerRule},
	})
	require.NoError(t, err)
	var lines []string
	for i := range res.Resources {
		r := &res.Resources[i]
		parts := []string{describe(r), strings.Join(r.Sources, ",")}
		for _, p := range r.Parents {
			parts = append(parts, "parent:"+p.ARN)
		}
		lines = append(lines, strings.Join(parts, " "))
	}
	var cov bytes.Buffer
	require.NoError(t, inventory.WriteCoverage(&cov, res.Coverage))
	out, err := json.MarshalIndent(map[string]any{"resources": lines, "runs": res.Runs}, "", "  ")
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join(goldenDir, "cc-parents.golden.json"), append(out, '\n'))
	testutil.Golden(t, filepath.Join(goldenDir, "cc-parents-coverage.golden"), cov.Bytes())
	require.Len(t, res.Resources, 3)
	for i, want := range []string{cfnListener, cfnListenerRule} {
		assert.Equal(t, want, res.Resources[i].CFNType)
		assert.Equal(t, []string{inventory.LayerCC}, res.Resources[i].Sources)
		assert.Len(t, res.Resources[i].Parents, 1)
	}
}

// TestCloudControlAuto skips the Cloud Control types an earlier layer listed in the same region,
// and marks a type whose parent no layer lists as a blind spot.
func TestCloudControlAuto(t *testing.T) {
	t.Parallel()

	e := env(t)
	e.CloudControlTypes = []string{cfnLB}
	rt := transport(t, "inventory-cc-auto", func(r *awstest.Router) {
		r.On("config", "SelectResourceConfig", "", ok(configPage("")))
	})
	res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: []inventory.Layer{
		inventory.NewConfig(e), inventory.NewCloudControl(e),
	}}).Collect(t.Context(), inventory.Request{
		Targets: []inventory.Target{{Account: account(rt), Report: oneRegion()}},
		Types:   []string{cfnLB},
	})
	require.NoError(t, err)
	require.Len(t, res.Runs, 2)
	assert.Equal(t, inventory.RunOK, res.Runs[0].Status)
	assert.Equal(t, inventory.RunSkipped, res.Runs[1].Status)

	e2 := env(t)
	e2.CloudControlTypes = []string{cfnListenerRule}
	assert.False(t, inventory.ParentCovered(e2.Map, cfnListenerRule, []inventory.Layer{inventory.NewCloudControl(e2)}))
	assert.True(t, inventory.ParentCovered(e2.Map, cfnLB, nil), "a type without a parent is covered")
}

// oneRegion is a probe result for eu-west-1 with a recording Config recorder.
func oneRegion() *probe.Report {
	r := twoRegions()
	r.Regions = r.Regions[1:]
	return r
}
