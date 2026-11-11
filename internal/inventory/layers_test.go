package inventory_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	cassettes = "../../testdata/cassettes/aws"
	acct      = awstest.FakeAccount
	euWest    = "eu-west-1"
	usEast    = "us-east-1"
	// policyDoc stands in for a policy document; no inventory output may contain it.
	policyDoc = "FIXTURE-POLICY-DOCUMENT"
)

type noBackoff struct{}

func (noBackoff) BackoffDelay(int, error) (time.Duration, error) { return 0, nil }

// account returns an account whose requests go to rt. Retries don't wait, so a throttled
// request is retried at once.
func account(rt http.RoundTripper) *accounts.Account {
	return &accounts.Account{ID: acct, Partition: "aws", Config: aws.Config{
		Region:      euWest,
		Credentials: credentials.NewStaticCredentialsProvider("AKIAFAKE", "fake-secret", ""),
		HTTPClient:  &http.Client{Transport: rt},
		Retryer: func() aws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.MaxAttempts = 3
				o.Backoff = noBackoff{}
			})
		},
	}}
}

func env(t *testing.T) *inventory.Env {
	t.Helper()
	m, err := typemap.Load()
	require.NoError(t, err)
	unlimited := map[string]rate.Limit{}
	for s := range inventory.DefaultRates() {
		unlimited[s] = rate.Inf
	}
	return &inventory.Env{Map: m, Limits: inventory.NewLimiters(unlimited)}
}

// transport replays the cassette name, or records it from the stubs build adds when goldens are
// being updated.
func transport(t *testing.T, name string, build func(*awstest.Router)) http.RoundTripper {
	t.Helper()
	path := filepath.Join(cassettes, name)
	if testutil.Updating() {
		r := awstest.NewRouter(t)
		build(r)
		return awstest.WriteCassette(t, path, r)
	}
	return awstest.Replay(t, path)
}

// describe renders a resource as one line of identifiers.
func describe(r *inventory.CloudResource) string {
	parts := []string{r.CFNType, cmpOr(r.ARN, r.ID)}
	if len(r.Tags) > 0 {
		var tags []string
		for k, v := range r.Tags {
			tags = append(tags, k+"="+v)
		}
		slices.Sort(tags)
		parts = append(parts, "tags:"+strings.Join(tags, ","))
	}
	if r.Region == "" {
		parts = append(parts, "global")
	}
	return strings.Join(parts, " ")
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func ok(body string) awstest.Stub { return awstest.Stub{Body: body} }

func fail(status int, body string) awstest.Stub { return awstest.Stub{Status: status, Body: body} }

// configRow is one SelectResourceConfig result, a JSON document inside the JSON response.
func configRow(cfg, arn, id, region string, tags ...string) string {
	var tagJSON []string
	for i := 0; i+1 < len(tags); i += 2 {
		tagJSON = append(tagJSON, fmt.Sprintf(`{"key":%q,"value":%q}`, tags[i], tags[i+1]))
	}
	row := fmt.Sprintf(
		`{"resourceId":%q,"resourceName":%q,"resourceType":%q,"arn":%q,"awsRegion":%q,"accountId":%q,"tags":[%s],"relationships":[]}`,
		id,
		id,
		cfg,
		arn,
		region,
		acct,
		strings.Join(tagJSON, ","),
	)
	return fmt.Sprintf("%q", row)
}

func configPage(next string, rows ...string) string {
	tok := ""
	if next != "" {
		tok = fmt.Sprintf(`,"NextToken":%q`, next)
	}
	return `{"Results":[` + strings.Join(rows, ",") + `]` + tok + `}`
}

func reResource(arn, reType, region string, tags ...string) string {
	var tagJSON []string
	for i := 0; i+1 < len(tags); i += 2 {
		tagJSON = append(tagJSON, fmt.Sprintf(`{"Key":%q,"Value":%q}`, tags[i], tags[i+1]))
	}
	props := ""
	if len(tagJSON) > 0 {
		props = `,"Properties":[{"Name":"tags","Data":[` + strings.Join(tagJSON, ",") + `]}]`
	}
	svc, _, _ := strings.Cut(reType, ":")
	return fmt.Sprintf(
		`{"Arn":%q,"OwningAccountId":%q,"Region":%q,"ResourceType":%q,"Service":%q%s}`,
		arn,
		acct,
		region,
		reType,
		svc,
		props,
	)
}

const cfnQueue = "AWS::SQS::Queue"

func ccLayer(types ...string) func(*inventory.Env) inventory.Layer {
	return func(e *inventory.Env) inventory.Layer {
		e.CloudControlTypes = types
		return inventory.NewCloudControl(e)
	}
}

func queueURL(name string) string { return "https://sqs.eu-west-1.amazonaws.com/" + acct + "/" + name }

func queueARN(name string) string { return "arn:aws:sqs:eu-west-1:" + acct + ":" + name }

// ccResource is one Cloud Control resource description. A queue's identifier is its URL, so its
// properties carry the ARN; other identifiers are ARNs.
func ccResource(id string) string {
	props := `{}`
	if strings.HasPrefix(id, "https://sqs.") {
		name := id[strings.LastIndex(id, "/")+1:]
		props = fmt.Sprintf(`{"QueueUrl":%q,"Arn":%q,"QueueName":%q}`, id, queueARN(name), name)
	}
	return fmt.Sprintf(`{"Identifier":%q,"Properties":%q}`, id, props)
}

func ccPage(next string, ids ...string) string {
	resources := make([]string, len(ids))
	for i, id := range ids {
		resources[i] = ccResource(id)
	}
	tok := ""
	if next != "" {
		tok = fmt.Sprintf(`,"NextToken":%q`, next)
	}
	return `{"ResourceDescriptions":[` + strings.Join(resources, ",") + `]` + tok + `}`
}

func rePage(next string, resources ...string) string {
	tok := ""
	if next != "" {
		tok = fmt.Sprintf(`,"NextToken":%q`, next)
	}
	return `{"Resources":[` + strings.Join(resources, ",") + `]` + tok + `}`
}

// sgRule is one rule of security group sg-1.
func sgRule(id string, egress bool, proto string, from, to int, source string) string {
	const group = "sg-1"
	var src string
	switch {
	case strings.HasPrefix(source, "sg-"):
		src = "<referencedGroupInfo><groupId>" + source + "</groupId><userId>" + acct + "</userId></referencedGroupInfo>"
	case strings.HasPrefix(source, "pl-"):
		src = "<prefixListId>" + source + "</prefixListId>"
	default:
		src = "<cidrIpv4>" + source + "</cidrIpv4>"
	}
	return fmt.Sprintf(
		`<item><securityGroupRuleId>%s</securityGroupRuleId><groupId>%s</groupId><groupOwnerId>%s</groupOwnerId>`+
			`<isEgress>%t</isEgress><ipProtocol>%s</ipProtocol><fromPort>%d</fromPort><toPort>%d</toPort>%s`+
			`<securityGroupRuleArn>arn:aws:ec2:%s:%s:security-group-rule/%s</securityGroupRuleArn><tagSet/></item>`,
		id,
		group,
		acct,
		egress,
		proto,
		from,
		to,
		src,
		euWest,
		acct,
		id,
	)
}

func sgPage(next string, items ...string) string {
	tok := ""
	if next != "" {
		tok = "<nextToken>" + next + "</nextToken>"
	}
	return `<DescribeSecurityGroupRulesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>0</requestId>` +
		`<securityGroupRuleSet>` + strings.Join(
		items,
		"",
	) + `</securityGroupRuleSet>` + tok + `</DescribeSecurityGroupRulesResponse>`
}

func iamPage(marker, body string) string {
	trunc := "<IsTruncated>false</IsTruncated>"
	if marker != "" {
		trunc = "<IsTruncated>true</IsTruncated><Marker>" + marker + "</Marker>"
	}
	return `<GetAccountAuthorizationDetailsResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/"><GetAccountAuthorizationDetailsResult>` +
		trunc + body + `</GetAccountAuthorizationDetailsResult><ResponseMetadata><RequestId>0</RequestId></ResponseMetadata></GetAccountAuthorizationDetailsResponse>`
}

const readOnly = "arn:aws:iam::aws:policy/ReadOnlyAccess"

func iamRole(name string) string {
	return `<RoleDetailList><member><RoleName>` + name + `</RoleName><Arn>arn:aws:iam::` + acct + `:role/` + name + `</Arn>` +
		`<AttachedManagedPolicies><member><PolicyName>ReadOnlyAccess</PolicyName><PolicyArn>` + readOnly + `</PolicyArn></member></AttachedManagedPolicies>` +
		`<RolePolicyList><member><PolicyName>inline</PolicyName><PolicyDocument>` + policyDoc + `</PolicyDocument></member></RolePolicyList></member></RoleDetailList>`
}

func iamUserGroup() string {
	return `<UserDetailList><member><UserName>ci</UserName><Arn>arn:aws:iam::` + acct + `:user/ci</Arn>` +
		`<AttachedManagedPolicies><member><PolicyName>ReadOnlyAccess</PolicyName><PolicyArn>` + readOnly + `</PolicyArn></member></AttachedManagedPolicies>` +
		`</member></UserDetailList><GroupDetailList><member><GroupName>ops</GroupName><Arn>arn:aws:iam::` + acct + `:group/ops</Arn>` +
		`<GroupPolicyList><member><PolicyName>deploy</PolicyName><PolicyDocument>` + policyDoc + `</PolicyDocument></member></GroupPolicyList></member></GroupDetailList>`
}

const r53NS = `xmlns="https://route53.amazonaws.com/doc/2013-04-01/"`

func zones() string {
	return `<ListHostedZonesResponse ` + r53NS + `><HostedZones><HostedZone><Id>/hostedzone/Z1</Id><Name>example.com.</Name>` +
		`<CallerReference>r</CallerReference><ResourceRecordSetCount>3</ResourceRecordSetCount></HostedZone></HostedZones>` +
		`<IsTruncated>false</IsTruncated><MaxItems>100</MaxItems><Marker></Marker></ListHostedZonesResponse>`
}

func recordSet(name, typ, setID string) string {
	set := ""
	if setID != "" {
		set = "<SetIdentifier>" + setID + "</SetIdentifier><Weight>10</Weight>"
	}
	return `<ResourceRecordSet><Name>` + name + `</Name><Type>` + typ + `</Type>` + set +
		`<TTL>300</TTL><ResourceRecords><ResourceRecord><Value>192.0.2.1</Value></ResourceRecord></ResourceRecords></ResourceRecordSet>`
}

func records(next string, sets ...string) string {
	trunc := "<IsTruncated>false</IsTruncated>"
	if next != "" {
		name, typ, _ := strings.Cut(next, " ")
		trunc = "<IsTruncated>true</IsTruncated><NextRecordName>" + name + "</NextRecordName><NextRecordType>" + typ + "</NextRecordType>"
	}
	return `<ListResourceRecordSetsResponse ` + r53NS + `><ResourceRecordSets>` + strings.Join(
		sets,
		"",
	) + `</ResourceRecordSets>` +
		trunc + `<MaxItems>2</MaxItems></ListResourceRecordSetsResponse>`
}

const s3NS = `xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`

func buckets(next string, names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(
			"<Bucket><Name>" + n + "</Name><CreationDate>2026-01-01T00:00:00.000Z</CreationDate><BucketRegion>" + euWest + "</BucketRegion></Bucket>",
		)
	}
	tok := ""
	if next != "" {
		tok = "<ContinuationToken>" + next + "</ContinuationToken>"
	}
	return `<ListAllMyBucketsResult ` + s3NS + `><Owner><ID>o</ID></Owner><Buckets>` + b.String() + `</Buckets>` + tok + `</ListAllMyBucketsResult>`
}

// bucketConfig stubs a bucket with a policy, versioning, default encryption and a public access
// block, and no lifecycle.
func bucketConfig(r *awstest.Router, bucket string) {
	m := bucket + "."
	r.On("s3", "GetBucketPolicy", m, ok(`{"Version":"2012-10-17","Statement":[]}`))
	r.On(
		"s3",
		"GetBucketVersioning",
		m,
		ok(`<VersioningConfiguration `+s3NS+`><Status>Enabled</Status></VersioningConfiguration>`),
	)
	r.On(
		"s3",
		"GetBucketLifecycleConfiguration",
		m,
		fail(http.StatusNotFound, awstest.S3Error("NoSuchLifecycleConfiguration", "none")),
	)
	r.On(
		"s3",
		"GetBucketEncryption",
		m,
		ok(`<ServerSideEncryptionConfiguration `+s3NS+`><Rule><ApplyServerSideEncryptionByDefault>`+
			`<SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault></Rule></ServerSideEncryptionConfiguration>`),
	)
	r.On(
		"s3",
		"GetPublicAccessBlock",
		m,
		ok(`<PublicAccessBlockConfiguration `+s3NS+`><BlockPublicAcls>true</BlockPublicAcls>`+
			`<IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets>`+
			`</PublicAccessBlockConfiguration>`),
	)
}

type scenario struct {
	stubs  func(*awstest.Router)
	want   []string // described resources
	denied bool     // one AccessDenied error and nothing else
}

type layerCase struct {
	name      string
	layer     func(*inventory.Env) inventory.Layer
	region    string
	scenarios map[string]scenario
}

func nativeLayer(name string) func(*inventory.Env) inventory.Layer {
	return func(e *inventory.Env) inventory.Layer {
		for _, l := range inventory.NativeLayers(e) {
			if l.Name() == name {
				return l
			}
		}
		panic("no native layer " + name)
	}
}

func layerCases() []layerCase {
	vpc := "arn:aws:ec2:eu-west-1:" + acct + ":vpc/vpc-1"
	sg := "arn:aws:ec2:eu-west-1:" + acct + ":security-group/sg-1"
	role := "arn:aws:iam::" + acct + ":role/app"
	denyJSON := fail(http.StatusBadRequest, awstest.JSONError("AccessDeniedException", "not authorized"))
	return []layerCase{
		{
			name: "cloudcontrol", layer: ccLayer(cfnQueue), region: euWest,
			scenarios: map[string]scenario{
				"paginated": {
					stubs: func(r *awstest.Router) {
						r.On("cloudcontrolapi", "ListResources", "page-2", ok(ccPage("", queueURL("logs"))))
						r.On("cloudcontrolapi", "ListResources", "", ok(ccPage("page-2", queueURL("jobs"))))
					},
					want: []string{cfnQueue + " " + queueARN("jobs"), cfnQueue + " " + queueARN("logs")},
				},
				"throttled": {
					stubs: func(r *awstest.Router) {
						r.On("cloudcontrolapi", "ListResources", "",
							fail(http.StatusBadRequest, awstest.JSONError("ThrottlingException", "Rate exceeded")),
							ok(ccPage("", queueURL("jobs"))))
					},
					want: []string{cfnQueue + " " + queueARN("jobs")},
				},
				"denied": {
					stubs: func(r *awstest.Router) {
						r.On("cloudcontrolapi", "ListResources", "", denyJSON)
					},
					denied: true,
				},
			},
		},
		{
			name: "config", layer: inventory.NewConfig, region: euWest,
			scenarios: map[string]scenario{
				"paginated": {
					stubs: func(r *awstest.Router) {
						r.On("config", "SelectResourceConfig", "NextToken", ok(configPage("",
							configRow("AWS::IAM::Role", role, "AROAEXAMPLE", "global"))))
						r.On("config", "SelectResourceConfig", "", ok(configPage("page-2",
							configRow("AWS::EC2::VPC", vpc, "vpc-1", euWest, "Name", "main"),
							configRow("AWS::EC2::SecurityGroup", sg, "sg-1", euWest))))
					},
					want: []string{
						"AWS::EC2::VPC " + vpc + " tags:Name=main",
						"AWS::EC2::SecurityGroup " + sg,
						"AWS::IAM::Role " + role + " global",
					},
				},
				"throttled": {
					stubs: func(r *awstest.Router) {
						r.On("config", "SelectResourceConfig", "",
							fail(http.StatusBadRequest, awstest.JSONError("ThrottlingException", "Rate exceeded")),
							ok(configPage("", configRow("AWS::EC2::VPC", vpc, "vpc-1", euWest))))
					},
					want: []string{"AWS::EC2::VPC " + vpc},
				},
				"denied": {
					stubs:  func(r *awstest.Router) { r.On("config", "SelectResourceConfig", "", denyJSON) },
					denied: true,
				},
			},
		},
		{
			name: "re", layer: inventory.NewResourceExplorer, region: usEast,
			scenarios: map[string]scenario{
				"paginated": {
					stubs: func(r *awstest.Router) {
						r.On("resource-explorer-2", "ListResources", "NextToken", ok(rePage(
							"",
							reResource(role, "iam:role", "global"),
							reResource(
								"arn:aws:quicksight:us-east-1:"+acct+":dashboard/d1",
								"quicksight:dashboard",
								usEast,
							),
						)))
						r.On("resource-explorer-2", "ListResources", "", ok(rePage("page-2",
							reResource("arn:aws:ec2:us-east-1:"+acct+":vpc/vpc-9", "ec2:vpc", usEast, "team", "net"))))
					},
					want: []string{
						"AWS::EC2::VPC arn:aws:ec2:us-east-1:" + acct + ":vpc/vpc-9 tags:team=net",
						"AWS::IAM::Role " + role + " global",
						" arn:aws:quicksight:us-east-1:" + acct + ":dashboard/d1",
					},
				},
				"throttled": {
					stubs: func(r *awstest.Router) {
						r.On("resource-explorer-2", "ListResources", "",
							fail(http.StatusTooManyRequests, awstest.JSONError("ThrottlingException", "Rate exceeded")),
							ok(rePage("", reResource(role, "iam:role", "global"))))
					},
					want: []string{"AWS::IAM::Role " + role + " global"},
				},
				"denied": {
					stubs: func(r *awstest.Router) {
						r.On("resource-explorer-2", "ListResources", "",
							fail(http.StatusForbidden, awstest.JSONError("AccessDeniedException", "not authorized")))
					},
					denied: true,
				},
			},
		},
		{
			name: "sg-rules", layer: nativeLayer(inventory.LayerSGRules), region: euWest,
			scenarios: map[string]scenario{
				"paginated": {
					stubs: func(r *awstest.Router) {
						r.On("ec2", "DescribeSecurityGroupRules", "NextToken=page-2", ok(sgPage("",
							sgRule("sgr-3", false, "tcp", 5432, 5432, "pl-1"))))
						r.On("ec2", "DescribeSecurityGroupRules", "", ok(sgPage("page-2",
							sgRule("sgr-1", false, "tcp", 443, 443, "10.0.0.0/8"),
							sgRule("sgr-2", true, "-1", -1, -1, "sg-2"))))
					},
					want: []string{
						"AWS::EC2::SecurityGroupIngress arn:aws:ec2:eu-west-1:" + acct + ":security-group-rule/sgr-1",
						"AWS::EC2::SecurityGroupEgress arn:aws:ec2:eu-west-1:" + acct + ":security-group-rule/sgr-2",
						"AWS::EC2::SecurityGroupIngress arn:aws:ec2:eu-west-1:" + acct + ":security-group-rule/sgr-3",
					},
				},
				"throttled": {
					stubs: func(r *awstest.Router) {
						r.On(
							"ec2",
							"DescribeSecurityGroupRules",
							"",
							fail(
								http.StatusServiceUnavailable,
								awstest.EC2Error("RequestLimitExceeded", "Request limit exceeded."),
							),
							ok(sgPage("", sgRule("sgr-1", false, "tcp", 443, 443, "10.0.0.0/8"))),
						)
					},
					want: []string{
						"AWS::EC2::SecurityGroupIngress arn:aws:ec2:eu-west-1:" + acct + ":security-group-rule/sgr-1",
					},
				},
				"denied": {
					stubs: func(r *awstest.Router) {
						r.On(
							"ec2",
							"DescribeSecurityGroupRules",
							"",
							fail(
								http.StatusForbidden,
								awstest.EC2Error("UnauthorizedOperation", "You are not authorized"),
							),
						)
					},
					denied: true,
				},
			},
		},
		{
			name: "iam-attachments", layer: nativeLayer(inventory.LayerIAMAttachments),
			scenarios: map[string]scenario{
				"paginated": {
					stubs: func(r *awstest.Router) {
						r.On("iam", "GetAccountAuthorizationDetails", "Marker=page-2", ok(iamPage("", iamUserGroup())))
						r.On("iam", "GetAccountAuthorizationDetails", "", ok(iamPage("page-2", iamRole("app"))))
					},
					want: []string{
						"Native::IAM::RolePolicyAttachment app/" + readOnly + " global",
						"AWS::IAM::RolePolicy app:inline global",
						"Native::IAM::UserPolicyAttachment ci/" + readOnly + " global",
						"AWS::IAM::GroupPolicy ops:deploy global",
					},
				},
				"throttled": {
					stubs: func(r *awstest.Router) {
						r.On("iam", "GetAccountAuthorizationDetails", "",
							fail(http.StatusBadRequest, awstest.QueryError("Throttling", "Rate exceeded")),
							ok(iamPage("", iamRole("app"))))
					},
					want: []string{
						"Native::IAM::RolePolicyAttachment app/" + readOnly + " global",
						"AWS::IAM::RolePolicy app:inline global",
					},
				},
				"denied": {
					stubs: func(r *awstest.Router) {
						r.On("iam", "GetAccountAuthorizationDetails", "",
							fail(http.StatusForbidden, awstest.QueryError("AccessDenied", "not authorized")))
					},
					denied: true,
				},
			},
		},
		{
			name: "route53-records", layer: nativeLayer(inventory.LayerRoute53Records),
			scenarios: map[string]scenario{
				"paginated": {
					stubs: func(r *awstest.Router) {
						r.On("route53", "ListHostedZones", "", ok(zones()))
						r.On("route53", "ListResourceRecordSets", "name=", ok(records("",
							recordSet(`\052.example.com.`, "CNAME", ""), recordSet("www.example.com.", "A", "blue"))))
						r.On("route53", "ListResourceRecordSets", "", ok(records(`\052.example.com. CNAME`,
							recordSet("example.com.", "NS", ""), recordSet("example.com.", "SOA", ""))))
					},
					want: []string{
						"AWS::Route53::RecordSet Z1_example.com_NS global",
						"AWS::Route53::RecordSet Z1_example.com_SOA global",
						"AWS::Route53::RecordSet Z1_*.example.com_CNAME global",
						"AWS::Route53::RecordSet Z1_www.example.com_A_blue global",
					},
				},
				"throttled": {
					stubs: func(r *awstest.Router) {
						r.On("route53", "ListHostedZones", "",
							fail(http.StatusBadRequest, awstest.QueryError("Throttling", "Rate exceeded")), ok(zones()))
						r.On(
							"route53",
							"ListResourceRecordSets",
							"",
							ok(records("", recordSet("example.com.", "NS", ""))),
						)
					},
					want: []string{"AWS::Route53::RecordSet Z1_example.com_NS global"},
				},
				"denied": {
					stubs: func(r *awstest.Router) {
						r.On(
							"route53",
							"ListHostedZones",
							"",
							fail(http.StatusForbidden, awstest.QueryError("AccessDenied", "not authorized")),
						)
					},
					denied: true,
				},
			},
		},
		{
			name: "s3-subresources", layer: nativeLayer(inventory.LayerS3Subresources), region: euWest,
			scenarios: map[string]scenario{
				"paginated": {
					stubs: func(r *awstest.Router) {
						r.On("s3", "ListBuckets", "continuation-token=page-2", ok(buckets("", "acme-web")))
						r.On("s3", "ListBuckets", "", ok(buckets("page-2", "acme-logs")))
						bucketConfig(r, "acme-logs")
						bucketConfig(r, "acme-web")
					},
					want: []string{
						"AWS::S3::BucketPolicy acme-logs", "Native::S3::BucketVersioning acme-logs",
						"Native::S3::BucketEncryption acme-logs", "Native::S3::BucketPublicAccessBlock acme-logs",
						"AWS::S3::BucketPolicy acme-web", "Native::S3::BucketVersioning acme-web",
						"Native::S3::BucketEncryption acme-web", "Native::S3::BucketPublicAccessBlock acme-web",
					},
				},
				"throttled": {
					stubs: func(r *awstest.Router) {
						r.On(
							"s3",
							"ListBuckets",
							"",
							fail(
								http.StatusServiceUnavailable,
								awstest.S3Error("SlowDown", "Please reduce your request rate."),
							),
							ok(buckets("", "acme-logs")),
						)
						bucketConfig(r, "acme-logs")
					},
					want: []string{
						"AWS::S3::BucketPolicy acme-logs", "Native::S3::BucketVersioning acme-logs",
						"Native::S3::BucketEncryption acme-logs", "Native::S3::BucketPublicAccessBlock acme-logs",
					},
				},
				"denied": {
					stubs: func(r *awstest.Router) {
						r.On(
							"s3",
							"ListBuckets",
							"",
							fail(http.StatusForbidden, awstest.S3Error("AccessDenied", "Access Denied")),
						)
					},
					denied: true,
				},
			},
		},
	}
}

// TestSpec_inventory_1: each layer has stubbed-response tests. It runs each layer against a paginated listing, a throttled call that succeeds on
// retry, and an AccessDenied.
func TestSpec_inventory_1(t *testing.T) {
	t.Parallel()

	for _, lc := range layerCases() {
		for _, name := range []string{"paginated", "throttled", "denied"} {
			sc := lc.scenarios[name]
			t.Run(lc.name+"/"+name, func(t *testing.T) {
				t.Parallel()
				rt := transport(t, "inventory-"+lc.name+"-"+name, sc.stubs)
				e := env(t)
				var got []string
				var errs []error
				for r, err := range lc.layer(e).List(t.Context(), account(rt), lc.region, nil) {
					if err != nil {
						errs = append(errs, err)
						continue
					}
					got = append(got, describe(&r))
					assert.NotContains(t, fmt.Sprint(r), policyDoc)
					assert.Equal(t, []string{lc.layer(e).Name()}, r.Sources)
				}
				if sc.denied {
					require.Len(t, errs, 1)
					assert.True(t, inventory.Denied(errs[0]), errs[0])
					assert.Empty(t, got)
					return
				}
				require.Empty(t, errs)
				assert.Equal(t, sc.want, got)
			})
		}
	}
}
