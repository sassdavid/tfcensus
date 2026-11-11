package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const (
	scanCassette    = cassetteDir + "/aws/scan-fleet"
	scanIAMCassette = cassetteDir + "/aws/scan-iam"
	goldenScan      = "../../testdata/golden/scan"
	scanManifest    = "../../testdata/states/scan/manifest.txt"
	scanConfig      = reposDir + "/tf-scan/tfcensus.yaml"
	scanAcct        = awstest.FakeAccount
	scanRegion      = "eu-west-1"
)

func scanNow() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }

// splitTransport sends STS and bucket requests (state reads) to the fake, and every other AWS
// request, ListBuckets included, to the router.
type splitTransport struct {
	fake   *awstest.Fake
	router *awstest.Router
}

func (s splitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Host, "sts.") || strings.Contains(req.URL.Host, ".s3.") {
		return s.fake.RoundTrip(req)
	}
	return s.router.RoundTrip(req)
}

func arnEC2(resource string) string {
	return "arn:aws:ec2:" + scanRegion + ":" + scanAcct + ":" + resource
}

func arnIAM(resource string) string { return "arn:aws:iam::" + scanAcct + ":" + resource }

func arnELB(resource string) string {
	return "arn:aws:elasticloadbalancing:" + scanRegion + ":" + scanAcct + ":" + resource
}

// item is one AWS Config result row, as the config layer selects it.
type item struct {
	Type, ARN, ID, Name, Region, Created string
	Tags                                 map[string]string
	Config                               map[string]any
	Rels                                 [][3]string // resource type, ID, relationship name
}

func (it item) row() string {
	tags := []map[string]string{}
	for k, v := range it.Tags {
		tags = append(tags, map[string]string{"key": k, "value": v})
	}
	rels := []map[string]string{}
	for _, r := range it.Rels {
		rels = append(rels, map[string]string{"resourceType": r[0], "resourceId": r[1], "relationshipName": r[2]})
	}
	m := map[string]any{
		"resourceId": it.ID, "resourceName": it.Name, "resourceType": it.Type, "arn": it.ARN,
		"awsRegion": it.Region, "accountId": scanAcct, "tags": tags, "relationships": rels,
	}
	if it.Created != "" {
		m["resourceCreationTime"] = it.Created
	}
	if it.Config != nil {
		m["configuration"] = it.Config
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// scanItems are the Config items of the stubbed account: one or more resources per category.
func scanItems() []item {
	defaultVPC := "vpc-0def00000000000a1"
	g := "global"
	return []item{
		{
			Type:   "AWS::EC2::VPC",
			ARN:    arnEC2("vpc/vpc-0a1b2c3d4e5f60718"),
			ID:     "vpc-0a1b2c3d4e5f60718",
			Region: scanRegion,
			Config: map[string]any{"isDefault": false},
		},
		{
			Type: "AWS::EC2::VPC", ARN: arnEC2("vpc/" + defaultVPC), ID: defaultVPC, Region: scanRegion,
			Config: map[string]any{"isDefault": true},
		},
		{
			Type:   "AWS::EC2::SecurityGroup",
			ARN:    arnEC2("security-group/sg-0aa11bb22cc33dd44"),
			ID:     "sg-0aa11bb22cc33dd44",
			Name:   "web",
			Region: scanRegion,
			Rels:   [][3]string{{"AWS::EC2::VPC", "vpc-0a1b2c3d4e5f60718", "Is contained in Vpc"}},
		},
		{
			Type:   "AWS::EC2::SecurityGroup",
			ARN:    arnEC2("security-group/sg-0def00000000000b2"),
			ID:     "sg-0def00000000000b2",
			Name:   "default",
			Region: scanRegion,
			Rels:   [][3]string{{"AWS::EC2::VPC", defaultVPC, "Is contained in Vpc"}},
		},
		{
			Type:    "AWS::EC2::SecurityGroup",
			ARN:     arnEC2("security-group/sg-0c11c0b5000000003"),
			ID:      "sg-0c11c0b5000000003",
			Name:    "clickops-debug",
			Region:  scanRegion,
			Created: "2026-10-01T09:00:00.000Z",
			Tags:    map[string]string{"owner": "alice"},
		},
		{
			Type:   "AWS::EC2::InternetGateway",
			ARN:    arnEC2("internet-gateway/igw-0def00000000000c3"),
			ID:     "igw-0def00000000000c3",
			Region: scanRegion,
			Rels:   [][3]string{{"AWS::EC2::VPC", defaultVPC, "Is attached to Vpc"}},
		},
		{
			Type:   "AWS::EC2::NetworkInterface",
			ARN:    arnEC2("network-interface/eni-0aa00000000000d4"),
			ID:     "eni-0aa00000000000d4",
			Region: scanRegion,
			Config: map[string]any{"requesterManaged": true, "interfaceType": "nat_gateway"},
		},
		{
			Type:   "AWS::EC2::Instance",
			ARN:    arnEC2("instance/i-0aa00000000000e5"),
			ID:     "i-0aa00000000000e5",
			Region: scanRegion,
			Tags:   map[string]string{"owner": "sandbox", "Name": "try-arm"},
		},
		{
			Type:   "AWS::KMS::Key",
			ARN:    "arn:aws:kms:eu-west-1:" + scanAcct + ":key/0b1c2d3e-aws0-4000-8000-000000000001",
			ID:     "0b1c2d3e-aws0-4000-8000-000000000001",
			Region: scanRegion,
			Config: map[string]any{"keyManager": "AWS"},
		},
		{
			Type:   "AWS::SQS::Queue",
			ARN:    "arn:aws:sqs:eu-west-1:" + scanAcct + ":billing-events",
			ID:     "https://sqs.eu-west-1.amazonaws.com/" + scanAcct + "/billing-events",
			Name:   "billing-events",
			Region: scanRegion,
			Tags:   map[string]string{"aws:cloudformation:stack-name": "billing"},
		},
		{
			Type: "AWS::S3::Bucket", ARN: "arn:aws:s3:::acme-growth-experiments", ID: "acme-growth-experiments",
			Name: "acme-growth-experiments", Region: scanRegion, Tags: map[string]string{"team": "growth"},
		},
		{
			Type: "AWS::ElasticLoadBalancingV2::LoadBalancer", ARN: arnELB("loadbalancer/app/web/50dc6c495c0c9188"),
			ID: arnELB("loadbalancer/app/web/50dc6c495c0c9188"), Name: "web", Region: scanRegion,
		},
		{
			Type:   "AWS::ElasticLoadBalancingV2::Listener",
			ARN:    arnELB("listener/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2"),
			ID:     arnELB("listener/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2"),
			Region: scanRegion,
		},
		{
			Type:   "AWS::Lambda::Function",
			ARN:    "arn:aws:lambda:eu-west-1:" + scanAcct + ":function:acme-api",
			ID:     "acme-api",
			Name:   "acme-api",
			Region: scanRegion,
		},
		{Type: "AWS::IAM::Role", ARN: arnIAM("role/acme-app"), ID: "AROAACMEAPP00000001", Name: "acme-app", Region: g},
		{
			Type:   "AWS::IAM::Role",
			ARN:    arnIAM("role/acme-shared"),
			ID:     "AROAACMESHARED00002",
			Name:   "acme-shared",
			Region: g,
		},
		{
			Type: "AWS::IAM::Role", ARN: arnIAM("role/aws-service-role/support.amazonaws.com/AWSServiceRoleForSupport"),
			ID: "AROASUPPORT00000003", Name: "AWSServiceRoleForSupport", Region: g,
		},
		{
			Type:    "AWS::IAM::Role",
			ARN:     arnIAM("role/clickops-admin"),
			ID:      "AROACLICKOPS0000004",
			Name:    "clickops-admin",
			Region:  g,
			Created: "2026-08-12T15:30:00.000Z",
			Tags:    map[string]string{"created-by": "bob"},
		},
		{
			Type: "AWS::IAM::User", ARN: arnIAM("user/sandbox-user"), ID: "AIDASANDBOX00000005", Name: "sandbox-user",
			Region: g, Tags: map[string]string{"owner": "sandbox"},
		},
	}
}

func configPageBody(items []item) string {
	rows := make([]string, len(items))
	for i, it := range items {
		rows[i] = fmt.Sprintf("%q", it.row())
	}
	return `{"Results":[` + strings.Join(rows, ",") + `]}`
}

func reResourceBody(arn, reType string) string {
	svc, _, _ := strings.Cut(reType, ":")
	return fmt.Sprintf(`{"Arn":%q,"OwningAccountId":%q,"Region":%q,"ResourceType":%q,"Service":%q}`,
		arn, scanAcct, scanRegion, reType, svc)
}

// scanStubs serves the probe, every inventory layer and Cloud Control for the scan cassette. With
// iamOnly, it serves a scan limited to IAM types: Config returns IAM items only and the layers
// without IAM types make no calls.
func scanStubs(r *awstest.Router, iamOnly bool) {
	ok := func(body string) awstest.Stub { return awstest.Stub{Body: body} }
	view := "arn:aws:resource-explorer-2:eu-west-1:" + scanAcct + ":view/all-resources/5d3c2b1a-0f9e-4d8c-b7a6-000000000001"
	r.On("resource-explorer-2", "ListIndexes", "", ok(`{"Indexes":[{"Arn":"arn:aws:resource-explorer-2:eu-west-1:`+
		scanAcct+`:index/0c7a9f4e-1b2d-4e5f-8a9b-000000000001","Region":"eu-west-1","Type":"LOCAL"}]}`))
	r.On("resource-explorer-2", "ListSupportedResourceTypes", "", ok(`{"ResourceTypes":[`+
		`{"ResourceType":"ec2:key-pair","Service":"ec2"},{"ResourceType":"logs:log-group","Service":"logs"}]}`))
	r.On("resource-explorer-2", "ListViews", "", ok(`{"Views":["`+view+`"]}`))
	r.On("config", "DescribeConfigurationAggregators", "", ok(`{"ConfigurationAggregators":[]}`))
	r.On("resource-explorer-2", "GetDefaultView", "", ok(`{"ViewArn":"`+view+`"}`))
	r.On("config", "DescribeConfigurationRecorders", "", ok(`{"ConfigurationRecorders":[{"name":"default",`+
		`"roleARN":"arn:aws:iam::`+scanAcct+`:role/aws-config","recordingGroup":{"allSupported":true,`+
		`"includeGlobalResourceTypes":true},"recordingMode":{"recordingFrequency":"CONTINUOUS"}}]}`))
	r.On("config", "DescribeConfigurationRecorderStatus", "", ok(`{"ConfigurationRecordersStatus":[{"name":"default",`+
		`"recording":true,"lastStatus":"SUCCESS"}]}`))
	items := scanItems()
	if iamOnly {
		items = slices.DeleteFunc(items, func(it item) bool { return !strings.HasPrefix(it.Type, "AWS::IAM::") })
	}
	r.On("config", "SelectResourceConfig", "", ok(configPageBody(items)))
	r.On("iam", "GetAccountAuthorizationDetails", "", ok(`<GetAccountAuthorizationDetailsResponse `+
		`xmlns="https://iam.amazonaws.com/doc/2010-05-08/"><GetAccountAuthorizationDetailsResult>`+
		`<IsTruncated>false</IsTruncated><UserDetailList/><GroupDetailList/><RoleDetailList/><Policies/>`+
		`</GetAccountAuthorizationDetailsResult></GetAccountAuthorizationDetailsResponse>`))
	if iamOnly {
		r.On("resource-explorer-2", "ListResources", "", ok(`{"Resources":[]}`))
		return
	}
	r.On("resource-explorer-2", "ListResources", "", ok(`{"Resources":[`+
		reResourceBody("arn:aws:logs:eu-west-1:"+scanAcct+":log-group:/aws/lambda/acme-api", "logs:log-group")+","+
		reResourceBody(arnEC2("key-pair/key-0aa00000000000f6"), "ec2:key-pair")+`]}`))
	r.On("cloudcontrolapi", "ListResources", "ListenerArn", ok(`{"ResourceDescriptions":[{"Identifier":"`+
		arnELB("listener-rule/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2/9683b2d02a6cabee")+`","Properties":"{}"}]}`))
	r.On("ec2", "DescribeSecurityGroupRules", "", ok(`<DescribeSecurityGroupRulesResponse `+
		`xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><securityGroupRuleSet/></DescribeSecurityGroupRulesResponse>`))
	r.On(
		"route53",
		"ListHostedZones",
		"",
		ok(`<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">`+
			`<HostedZones/><IsTruncated>false</IsTruncated><MaxItems>100</MaxItems></ListHostedZonesResponse>`),
	)
	r.On("s3", "ListBuckets", "", ok(`<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
		`<Owner><ID>fake-owner</ID></Owner><Buckets/></ListAllMyBucketsResult>`))
}

func scanDeps(t *testing.T, rt http.RoundTripper, home string, stdout, stderr *bytes.Buffer) *app.Deps {
	t.Helper()
	cwd, err := filepath.Abs(reposDir)
	require.NoError(t, err)
	return &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
		Env: app.Env{
			"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "NO_COLOR=1", "AWS_EC2_METADATA_DISABLED=true",
		},
		Clock:         scanNow,
		Getwd:         func() (string, error) { return cwd, nil },
		HTTPTransport: rt,
	}
}

// TestScanCassette writes the scan cassettes from the in-process S3 and STS and the router's
// stubs, when golden files are updated: scan-fleet for a full scan, scan-iam for --types
// 'AWS::IAM::*'. The scan testscripts replay them.
func TestScanCassette(t *testing.T) { //nolint:paralleltest // writes the cassettes the scripts read
	//nolint:paralleltest // the scripts replay these cassettes once the serial tests finish
	for _, tc := range []struct {
		cassette string
		iamOnly  bool
	}{{scanCassette, false}, {scanIAMCassette, true}} {
		t.Run(filepath.Base(tc.cassette), func(t *testing.T) {
			if !testutil.Updating() {
				t.Skip("replayed by the scan scripts; run mise run test:golden -- -update to rewrite it")
			}
			fake := awstest.NewFake(t, time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC))
			fake.LoadManifest(scanManifest)
			fake.Keys = map[string]string{"AKIAFAKEDEFAULT": scanAcct}
			router := awstest.NewRouter(t)
			scanStubs(router, tc.iamOnly)
			rt := awstest.WriteCassette(t, tc.cassette, splitTransport{fake: fake, router: router})
			args := []string{"scan", "tf-scan", "--config", scanConfig, "--format", "json", "--concurrency", "1"}
			if tc.iamOnly {
				args = append(args, "--types", "AWS::IAM::*")
			}
			var stdout, stderr bytes.Buffer
			code := cli.Execute(t.Context(), scanDeps(t, rt, fleetHome(t), &stdout, &stderr), args)
			require.Equal(t, cli.ExitOK, code, stderr.String())
		})
	}
}

// countingTransport counts the requests that reach rt.
type countingTransport struct {
	rt http.RoundTripper
	n  atomic.Int64
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.rt.RoundTrip(req)
}

// TestSpec_scan_4: a re-run with different --rules reads the inventory and state index from the
// cache and makes no AWS calls.
func TestSpec_scan_4(t *testing.T) {
	t.Parallel()

	home := fleetHome(t)
	args := []string{"scan", "tf-scan", "--config", scanConfig}
	run := func(extra ...string) (cliResult, int64) {
		ct := &countingTransport{rt: awstest.Replay(t, scanCassette)}
		var stdout, stderr bytes.Buffer
		code := cli.Execute(t.Context(), scanDeps(t, ct, home, &stdout, &stderr), append(slices.Clone(args), extra...))
		return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}, ct.n.Load()
	}
	first, calls := run("--format", "json")
	require.Equal(t, cli.ExitOK, first.code, first.stderr)
	require.Positive(t, calls, "the first scan lists the account")
	assert.Contains(t, first.stdout, `"cached": false`)

	rules := filepath.Join(goldenScan, "hide-debug-sg.yaml")
	second, calls := run("--rules", rules, "--format", "tty")
	require.Equal(t, cli.ExitOK, second.code, second.stderr)
	assert.Zero(t, calls, "the re-run with other rules makes no AWS calls")
	testutil.Golden(t, filepath.Join(goldenScan, "rerun-cached.txt"), []byte(second.stdout))
	assert.Contains(t, second.stdout, "4 unmanaged")
	assert.Contains(t, second.stdout, "(inventory from cache)")

	third, calls := run("--rules", rules, "--format", "tty", "--refresh")
	require.Equal(t, cli.ExitOK, third.code, third.stderr)
	assert.Positive(t, calls, "--refresh lists again")
	assert.NotContains(t, third.stdout, "(inventory from cache)")
}

// TestScanAttributeValues greps every scan output, the debug log, the explain trace and the
// scan cache for attribute values of the fixture states. Only identifiers may appear.
func TestScanAttributeValues(t *testing.T) {
	t.Parallel()

	secrets := []string{"FIXTURE-SECRET", "10.42.0.0/16"}
	home := fleetHome(t)
	outputs := map[string]string{}
	run := func(args ...string) {
		var stdout, stderr bytes.Buffer
		full := append([]string{"scan", "tf-scan", "--config", scanConfig, "-vv"}, args...)
		code := cli.Execute(t.Context(), scanDeps(t, awstest.Replay(t, scanCassette), home, &stdout, &stderr), full)
		require.Equal(t, cli.ExitOK, code, stderr.String())
		outputs["stdout "+strings.Join(args, " ")] = stdout.String()
		outputs["stderr "+strings.Join(args, " ")] = stderr.String()
	}
	for _, format := range []string{"json", "tty", "md", "csv", "sarif"} {
		run("--format", format, "--refresh")
	}
	run("--explain", "arn:aws:lambda:eu-west-1:111111111111:function:acme-api", "--format", "json")
	run("--explain-coverage")
	files, err := filepath.Glob(filepath.Join(home, ".cache", "tfcensus", "scan", "*.json.zst"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	var snap map[string]any
	require.NoError(t, tfstate.ReadIndexFile(files[0], &snap))
	b, err := json.Marshal(snap)
	require.NoError(t, err)
	outputs["cache"] = string(b)
	assert.Contains(t, outputs["cache"], "sg-0aa11bb22cc33dd44", "identifiers are kept")
	for label, out := range outputs {
		for _, s := range secrets {
			assert.NotContains(t, out, s, "%s contains an attribute value", label)
		}
	}
}
