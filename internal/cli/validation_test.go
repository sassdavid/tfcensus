package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

const (
	validationDir      = "../../testdata/validation"
	validationCassette = cassetteDir + "/aws/validation"
)

// validationGroup is one false-positive group of the first real-account scan, as anonymized AWS
// responses: the records each layer returns for it.
type validationGroup struct {
	Group            string           `json:"group"`
	SpecCount        int              `json:"spec_count"` // findings of the group in the real scan
	Why              string           `json:"why"`        // what classifies it now
	Config           []validationItem `json:"config"`
	ResourceExplorer []struct {
		ARN  string `json:"arn"`
		Type string `json:"type"`
	} `json:"resource_explorer"`
	IAMRoles []struct {
		Name     string   `json:"name"`
		Path     string   `json:"path"`
		ID       string   `json:"id"`
		Attached []string `json:"attached"`
		Inline   []string `json:"inline"`
	} `json:"iam_roles"`
	SGRules []struct {
		ID          string `json:"id"`
		Group       string `json:"group"`
		Egress      bool   `json:"egress"`
		Protocol    string `json:"protocol"`
		From        int    `json:"from"`
		To          int    `json:"to"`
		CIDR        string `json:"cidr"`
		Peer        string `json:"peer"`
		Description string `json:"description"`
	} `json:"sg_rules"`
	NATGateways []struct {
		ID         string `json:"id"`
		RouteTable string `json:"route_table"`
	} `json:"nat_gateways"`
	Zones []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Records []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Value string `json:"value"`
			Alias bool   `json:"alias"`
		} `json:"records"`
	} `json:"zones"`
	Buckets []validationBucket `json:"buckets"`
	Stacks  []struct {
		Name      string `json:"name"`
		Resources []struct {
			Logical  string `json:"logical"`
			Type     string `json:"type"`
			Physical string `json:"physical"`
		} `json:"resources"`
	} `json:"stacks"`
}

type validationItem struct {
	Type          string            `json:"type"`
	ARN           string            `json:"arn"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Region        string            `json:"region"`
	Tags          map[string]string `json:"tags"`
	Configuration map[string]any    `json:"configuration"`
	Relationships [][3]string       `json:"relationships"`
}

type validationBucket struct {
	Name       string   `json:"name"`
	PolicySids []string `json:"policy_sids"`
	Versioning bool     `json:"versioning"`
	Encryption bool     `json:"encryption"`
	PAB        bool     `json:"pab"`
	Lifecycle  bool     `json:"lifecycle"`
}

func loadValidationGroups(t *testing.T) []validationGroup {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(validationDir, "groups", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 13, "one fixture per false-positive group")
	groups := make([]validationGroup, 0, len(files))
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		var g validationGroup
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		require.NoError(t, dec.Decode(&g), f)
		groups = append(groups, g)
	}
	return groups
}

// validationTransport sends STS and the state bucket to the in-process fake, the fixture buckets
// and every other service to the router.
type validationTransport struct {
	fake    *awstest.Fake
	router  *awstest.Router
	buckets []string
}

func (v validationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	bucket, _, _ := strings.Cut(req.URL.Host, ".")
	if slices.Contains(v.buckets, bucket) {
		return v.router.RoundTrip(req)
	}
	if strings.HasPrefix(req.URL.Host, "sts.") || strings.Contains(req.URL.Host, ".s3.") {
		return v.fake.RoundTrip(req)
	}
	return v.router.RoundTrip(req)
}

// TestValidationCassette writes the validation cassette from the group fixtures when golden files
// are updated; spec_scan_accuracy_9 replays it.
func TestValidationCassette(t *testing.T) { //nolint:paralleltest // writes the cassette the script reads
	if !testutil.Updating() {
		t.Skip("replayed by spec_scan_accuracy_9; run mise run test:golden -- -update to rewrite it")
	}
	groups := loadValidationGroups(t)
	fake := awstest.NewFake(t, time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC))
	fake.LoadManifest(filepath.Join(validationDir, "states", "manifest.txt"))
	fake.Keys = map[string]string{"AKIAFAKEDEFAULT": scanAcct}
	router := awstest.NewRouter(t)
	buckets := validationStubs(router, groups)
	rt := awstest.WriteCassette(
		t,
		validationCassette,
		validationTransport{fake: fake, router: router, buckets: buckets},
	)
	cwd, err := filepath.Abs(validationDir)
	require.NoError(t, err)
	home := fleetHome(t)
	var stdout, stderr bytes.Buffer
	d := &app.Deps{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Env: app.Env{
			"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "NO_COLOR=1", "AWS_EC2_METADATA_DISABLED=true",
		},
		Clock:         func() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) },
		Getwd:         func() (string, error) { return cwd, nil },
		HTTPTransport: rt,
	}
	code := cli.Execute(t.Context(), d, []string{
		"scan",
		"repo",
		"--config",
		filepath.Join(cwd, "repo", "tfcensus.yaml"),
		"--format",
		"json",
		"--concurrency",
		"1",
	})
	require.Equal(t, cli.ExitOK, code, stderr.String())
}

// TestSpec_scan_accuracy_9: the anonymized fixtures of all 13 false-positive groups scan with no
// unmanaged finding.
func TestSpec_scan_accuracy_9(t *testing.T) {
	t.Parallel()
	p := scriptParams(t, filepath.Join(scriptDir, "spec_scan_accuracy_9.txtar"))
	setup := p.Setup
	p.Setup = func(env *testscript.Env) error {
		if err := setup(env); err != nil {
			return err
		}
		dir, err := filepath.Abs(validationDir)
		if err != nil {
			return err
		}
		env.Setenv("VALIDATION", dir)
		return nil
	}
	testscript.Run(t, p)
}

// validationStubs serves the probe and every layer from the groups, and returns the fixture
// bucket names.
func validationStubs(r *awstest.Router, groups []validationGroup) []string {
	ok := func(body string) awstest.Stub { return awstest.Stub{Body: body} }
	probeStubs(r)
	f := collectFragments(groups)
	r.On("config", "SelectResourceConfig", "", ok(configPageBody(f.items)))
	r.On("config", "GetDiscoveredResourceCounts", "", ok(`{"resourceCounts":[],"totalDiscoveredResources":0}`))
	r.On("resource-explorer-2", "ListResources", "", ok(`{"Resources":[`+strings.Join(f.re, ",")+`]}`))
	r.On("iam", "GetAccountAuthorizationDetails", "", ok(`<GetAccountAuthorizationDetailsResponse `+
		`xmlns="https://iam.amazonaws.com/doc/2010-05-08/"><GetAccountAuthorizationDetailsResult>`+
		`<IsTruncated>false</IsTruncated><UserDetailList/><GroupDetailList/><RoleDetailList>`+strings.Join(f.roles, "")+
		`</RoleDetailList><Policies/></GetAccountAuthorizationDetailsResult></GetAccountAuthorizationDetailsResponse>`))
	const ec2NS = `xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"`
	r.On("ec2", "DescribeSecurityGroupRules", "", ok(`<DescribeSecurityGroupRulesResponse `+ec2NS+
		`><securityGroupRuleSet>`+strings.Join(f.rules, "")+`</securityGroupRuleSet></DescribeSecurityGroupRulesResponse>`))
	r.On("ec2", "DescribeNatGateways", "", ok(`<DescribeNatGatewaysResponse `+ec2NS+`><natGatewaySet>`+
		strings.Join(f.nats, "")+`</natGatewaySet></DescribeNatGatewaysResponse>`))
	const r53NS = `xmlns="https://route53.amazonaws.com/doc/2013-04-01/"`
	r.On("route53", "ListHostedZones", "", ok(`<ListHostedZonesResponse `+r53NS+`><HostedZones>`+
		strings.Join(
			f.zones,
			"",
		)+`</HostedZones><IsTruncated>false</IsTruncated><MaxItems>100</MaxItems></ListHostedZonesResponse>`))
	for _, id := range slices.Sorted(maps.Keys(f.records)) {
		r.On("route53", "ListResourceRecordSets", "/hostedzone/"+id+"/", ok(`<ListResourceRecordSetsResponse `+r53NS+
			`><ResourceRecordSets>`+strings.Join(f.records[id], "")+`</ResourceRecordSets><IsTruncated>false</IsTruncated>`+
			`<MaxItems>300</MaxItems></ListResourceRecordSetsResponse>`))
	}
	r.On("cloudformation", "ListStacks", "", ok("<ListStacksResponse "+cfnXMLNS+"><ListStacksResult><StackSummaries>"+
		strings.Join(f.stacks, "")+"</StackSummaries></ListStacksResult></ListStacksResponse>"))
	for name, members := range f.stackRes {
		r.On("cloudformation", "ListStackResources", "stack%2F"+name+"%2F", ok("<ListStackResourcesResponse "+cfnXMLNS+
			"><ListStackResourcesResult><StackResourceSummaries>"+strings.Join(members, "")+
			"</StackResourceSummaries></ListStackResourcesResult></ListStackResourcesResponse>"))
	}
	none := ok(`{"ResourceDescriptions":[]}`)
	// VPC associations are listed per service network, and there is none.
	r.On("cloudcontrolapi", "ListResources", "AWS::VpcLattice::", none, none, none)
	return bucketStubs(r, f.buckets)
}

// fragments are the response pieces of every group, joined into one response per operation.
type fragments struct {
	items                                 []item
	re, roles, rules, nats, zones, stacks []string
	buckets                               []validationBucket
	records, stackRes                     map[string][]string
}

func collectFragments(groups []validationGroup) fragments {
	f := fragments{records: map[string][]string{}, stackRes: map[string][]string{}}
	for i := range groups {
		g := &groups[i]
		for _, it := range g.Config {
			f.items = append(f.items, item{
				Type: it.Type, ARN: it.ARN, ID: it.ID, Name: it.Name, Region: it.Region, Tags: it.Tags,
				Config: it.Configuration, Rels: it.Relationships,
			})
		}
		for _, x := range g.ResourceExplorer {
			f.re = append(f.re, reResourceBody(x.ARN, x.Type))
		}
		for _, x := range g.IAMRoles {
			f.roles = append(f.roles, iamRoleXML(x.Name, x.Path, x.ID, x.Attached, x.Inline))
		}
		for _, x := range g.SGRules {
			f.rules = append(
				f.rules,
				sgRuleXML(x.ID, x.Group, x.Egress, x.Protocol, x.From, x.To, x.CIDR, x.Peer, x.Description),
			)
		}
		for _, x := range g.NATGateways {
			f.nats = append(f.nats, "<item><natGatewayId>"+x.ID+"</natGatewayId><state>available</state>"+
				"<availabilityMode>regional</availabilityMode><routeTableId>"+x.RouteTable+"</routeTableId></item>")
		}
		f.addZones(g)
		f.buckets = append(f.buckets, g.Buckets...)
		f.addStacks(g)
	}
	return f
}

func (f *fragments) addZones(g *validationGroup) {
	for _, z := range g.Zones {
		f.zones = append(f.zones, "<HostedZone><Id>/hostedzone/"+z.ID+"</Id><Name>"+z.Name+".</Name>"+
			"<CallerReference>validation</CallerReference></HostedZone>")
		for _, rec := range z.Records {
			f.records[z.ID] = append(f.records[z.ID], recordXML(rec.Name, rec.Type, rec.Value, rec.Alias))
		}
	}
}

func (f *fragments) addStacks(g *validationGroup) {
	for _, s := range g.Stacks {
		f.stacks = append(f.stacks, "<member><StackId>"+validationStackID(s.Name)+"</StackId><StackName>"+s.Name+
			"</StackName><CreationTime>2026-03-01T08:00:00Z</CreationTime><StackStatus>CREATE_COMPLETE</StackStatus></member>")
		for _, x := range s.Resources {
			f.stackRes[s.Name] = append(f.stackRes[s.Name], "<member><LogicalResourceId>"+x.Logical+
				"</LogicalResourceId><PhysicalResourceId>"+x.Physical+"</PhysicalResourceId><ResourceType>"+x.Type+
				"</ResourceType><LastUpdatedTimestamp>2026-03-01T08:05:00Z</LastUpdatedTimestamp>"+
				"<ResourceStatus>CREATE_COMPLETE</ResourceStatus></member>")
		}
	}
}

// probeStubs serves the capability probe of a recording account with a Resource Explorer view.
func probeStubs(r *awstest.Router) {
	ok := func(body string) awstest.Stub { return awstest.Stub{Body: body} }
	view := "arn:aws:resource-explorer-2:eu-west-1:" + scanAcct + ":view/all-resources/5d3c2b1a-0f9e-4d8c-b7a6-000000000002"
	r.On("resource-explorer-2", "ListIndexes", "", ok(`{"Indexes":[{"Arn":"arn:aws:resource-explorer-2:eu-west-1:`+
		scanAcct+`:index/0c7a9f4e-1b2d-4e5f-8a9b-000000000002","Region":"eu-west-1","Type":"LOCAL"}]}`))
	r.On("resource-explorer-2", "ListSupportedResourceTypes", "", ok(`{"ResourceTypes":[`+
		`{"ResourceType":"logs:log-group","Service":"logs"}]}`))
	r.On("resource-explorer-2", "ListViews", "", ok(`{"Views":["`+view+`"]}`))
	r.On("resource-explorer-2", "GetDefaultView", "", ok(`{"ViewArn":"`+view+`"}`))
	r.On("config", "DescribeConfigurationAggregators", "", ok(`{"ConfigurationAggregators":[]}`))
	r.On("config", "DescribeConfigurationRecorders", "", ok(`{"ConfigurationRecorders":[{"name":"default",`+
		`"roleARN":"arn:aws:iam::`+scanAcct+`:role/aws-config","recordingGroup":{"allSupported":true,`+
		`"includeGlobalResourceTypes":true},"recordingMode":{"recordingFrequency":"CONTINUOUS"}}]}`))
	r.On("config", "DescribeConfigurationRecorderStatus", "", ok(`{"ConfigurationRecordersStatus":[{"name":"default",`+
		`"recording":true,"lastStatus":"SUCCESS"}]}`))
}

// bucketStubs serves ListBuckets and each fixture bucket's configurations, and returns the names.
func bucketStubs(r *awstest.Router, buckets []validationBucket) []string {
	ok := func(body string) awstest.Stub { return awstest.Stub{Body: body} }
	missing := func(code string) awstest.Stub {
		return awstest.Stub{Status: http.StatusNotFound, Body: awstest.S3Error(code, "not set")}
	}
	const s3NS = `xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`
	names := make([]string, 0, len(buckets))
	var list []string
	for _, b := range buckets {
		names = append(names, b.Name)
		list = append(list, "<Bucket><Name>"+b.Name+"</Name><CreationDate>2026-03-01T08:00:00Z</CreationDate>"+
			"<BucketRegion>eu-west-1</BucketRegion></Bucket>")
		m := b.Name + "."
		if len(b.PolicySids) > 0 {
			st := make([]string, 0, len(b.PolicySids))
			for _, sid := range b.PolicySids {
				st = append(
					st,
					`{"Sid":"`+sid+`","Effect":"Allow","Action":"s3:PutObject","Resource":"FIXTURE-SECRET-resource"}`,
				)
			}
			r.On("s3", "GetBucketPolicy", m, ok(`{"Version":"2012-10-17","Statement":[`+strings.Join(st, ",")+`]}`))
		} else {
			r.On("s3", "GetBucketPolicy", m, missing("NoSuchBucketPolicy"))
		}
		status := ""
		if b.Versioning {
			status = "<Status>Enabled</Status>"
		}
		r.On(
			"s3",
			"GetBucketVersioning",
			m,
			ok("<VersioningConfiguration "+s3NS+">"+status+"</VersioningConfiguration>"),
		)
		if b.Lifecycle {
			r.On(
				"s3",
				"GetBucketLifecycleConfiguration",
				m,
				ok("<LifecycleConfiguration "+s3NS+"><Rule><ID>expire</ID>"+
					"<Status>Enabled</Status><Filter/><Expiration><Days>30</Days></Expiration></Rule></LifecycleConfiguration>"),
			)
		} else {
			r.On("s3", "GetBucketLifecycleConfiguration", m, missing("NoSuchLifecycleConfiguration"))
		}
		if b.Encryption {
			r.On("s3", "GetBucketEncryption", m, ok("<ServerSideEncryptionConfiguration "+s3NS+"><Rule>"+
				"<ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault>"+
				"</Rule></ServerSideEncryptionConfiguration>"))
		} else {
			r.On("s3", "GetBucketEncryption", m, missing("ServerSideEncryptionConfigurationNotFoundError"))
		}
		if b.PAB {
			r.On("s3", "GetPublicAccessBlock", m, ok("<PublicAccessBlockConfiguration "+s3NS+">"+
				"<BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls>"+
				"<BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets>"+
				"</PublicAccessBlockConfiguration>"))
		} else {
			r.On("s3", "GetPublicAccessBlock", m, missing("NoSuchPublicAccessBlockConfiguration"))
		}
	}
	r.On("s3", "ListBuckets", "", ok("<ListAllMyBucketsResult "+s3NS+"><Owner><ID>fake-owner</ID></Owner><Buckets>"+
		strings.Join(list, "")+"</Buckets></ListAllMyBucketsResult>"))
	return names
}

func validationStackID(name string) string {
	return "arn:aws:cloudformation:eu-west-1:" + scanAcct + ":stack/" + name + "/6f1e2d3c-0000-4000-8000-000000000002"
}

func iamRoleXML(name, path, id string, attached, inline []string) string {
	var b strings.Builder
	b.WriteString("<member><Path>" + path + "</Path><RoleName>" + name + "</RoleName><RoleId>" + id + "</RoleId><Arn>" +
		arnIAM("role"+path+name) + "</Arn><CreateDate>2026-03-01T08:00:00Z</CreateDate><AttachedManagedPolicies>")
	for _, p := range attached {
		b.WriteString("<member><PolicyName>" + p[strings.LastIndex(p, "/")+1:] + "</PolicyName><PolicyArn>" + p +
			"</PolicyArn></member>")
	}
	b.WriteString("</AttachedManagedPolicies><RolePolicyList>")
	for _, p := range inline {
		b.WriteString("<member><PolicyName>" + p + "</PolicyName><PolicyDocument>FIXTURE-SECRET-document" +
			"</PolicyDocument></member>")
	}
	b.WriteString("</RolePolicyList></member>")
	return b.String()
}

func sgRuleXML(id, group string, egress bool, proto string, from, to int, cidr, peer, desc string) string {
	src := "<cidrIpv4>" + cidr + "</cidrIpv4>"
	if peer != "" {
		src = "<referencedGroupInfo><groupId>" + peer + "</groupId><userId>" + scanAcct + "</userId></referencedGroupInfo>"
	}
	return "<item><securityGroupRuleId>" + id + "</securityGroupRuleId><groupId>" + group + "</groupId><groupOwnerId>" +
		scanAcct + "</groupOwnerId><isEgress>" + strconv.FormatBool(egress) + "</isEgress><ipProtocol>" + proto +
		"</ipProtocol><fromPort>" + strconv.Itoa(from) + "</fromPort><toPort>" + strconv.Itoa(to) + "</toPort>" + src +
		"<description>" + desc + "</description></item>"
}

func recordXML(name, typ, value string, alias bool) string {
	body := "<TTL>300</TTL><ResourceRecords><ResourceRecord><Value>" + xmlEscape(value) +
		"</Value></ResourceRecord></ResourceRecords>"
	if alias {
		body = "<AliasTarget><HostedZoneId>Z32O12XQLNTSW2</HostedZoneId><DNSName>k8s-apps-0a1b2c3d.eu-west-1.elb." +
			"amazonaws.com.</DNSName><EvaluateTargetHealth>true</EvaluateTargetHealth></AliasTarget>"
	}
	return fmt.Sprintf("<ResourceRecordSet><Name>%s.</Name><Type>%s</Type>%s</ResourceRecordSet>", name, typ, body)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
