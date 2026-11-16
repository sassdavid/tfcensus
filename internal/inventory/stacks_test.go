package inventory_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

const cfnNS = `xmlns="http://cloudformation.amazonaws.com/doc/2010-05-15/"`

func stackID(name string) string {
	return "arn:aws:cloudformation:" + euWest + ":" + acct + ":stack/" + name + "/0b1c2d3e-0000-4000-8000-000000000001"
}

// stackSummary is one ListStacks member; root is the top-level stack's name for a nested stack.
func stackSummary(name, status, root string) string {
	s := "<member><StackId>" + stackID(name) + "</StackId><StackName>" + name +
		"</StackName><CreationTime>2026-06-01T08:00:00Z</CreationTime><StackStatus>" + status + "</StackStatus>"
	if root != "" {
		s += "<ParentId>" + stackID(root) + "</ParentId><RootId>" + stackID(root) + "</RootId>"
	}
	return s + "</member>"
}

func stacksPage(next string, members ...string) string {
	tok := ""
	if next != "" {
		tok = "<NextToken>" + next + "</NextToken>"
	}
	return "<ListStacksResponse " + cfnNS + "><ListStacksResult><StackSummaries>" + strings.Join(members, "") +
		"</StackSummaries>" + tok + "</ListStacksResult><ResponseMetadata><RequestId>00000000-0000-4000-8000-" +
		"000000000000</RequestId></ResponseMetadata></ListStacksResponse>"
}

// stackRes is one ListStackResources member.
func stackRes(logical, cfn, physical, status string) string {
	s := "<member><LogicalResourceId>" + logical + "</LogicalResourceId>"
	if physical != "" {
		s += "<PhysicalResourceId>" + physical + "</PhysicalResourceId>"
	}
	return s + "<ResourceType>" + cfn + "</ResourceType><LastUpdatedTimestamp>2026-06-01T08:05:00Z" +
		"</LastUpdatedTimestamp><ResourceStatus>" + status + "</ResourceStatus></member>"
}

func stackResPage(next string, members ...string) string {
	tok := ""
	if next != "" {
		tok = "<NextToken>" + next + "</NextToken>"
	}
	return "<ListStackResourcesResponse " + cfnNS + "><ListStackResourcesResult><StackResourceSummaries>" +
		strings.Join(members, "") + "</StackResourceSummaries>" + tok + "</ListStackResourcesResult>" +
		"<ResponseMetadata><RequestId>00000000-0000-4000-8000-000000000000</RequestId></ResponseMetadata>" +
		"</ListStackResourcesResponse>"
}

// stackParam is how a stack ID appears in a query protocol request body.
func stackParam(name string) string { return "stack%2F" + name + "%2F" }

// stackStubs serves two regions: eu-west-1 throttles the first ListStacks call, then has a root
// stack with paginated resources, a nested stack and a stack whose resources fail to list;
// us-east-1 denies ListStacks.
func stackStubs(r *awstest.Router) {
	r.On(
		"cloudformation",
		"ListStacks",
		"cloudformation.us-east-1.",
		fail(
			http.StatusForbidden,
			awstest.QueryError("AccessDenied", "not authorized to perform cloudformation:ListStacks"),
		),
	)
	r.On("cloudformation", "ListStacks", "NextToken=stacks-2", ok(stacksPage("",
		stackSummary("billing-AlarmsStack-1ABCDEF", "CREATE_COMPLETE", "billing"),
		stackSummary("gone-soon", "DELETE_FAILED", ""))))
	r.On("cloudformation", "ListStacks", "",
		fail(http.StatusBadRequest, awstest.QueryError("Throttling", "Rate exceeded")),
		ok(stacksPage("stacks-2", stackSummary("billing", "UPDATE_COMPLETE", ""))))
	r.On("cloudformation", "ListStackResources", "NextToken=res-2", ok(stackResPage("",
		stackRes("AlarmsStack", "AWS::CloudFormation::Stack", stackID("billing-AlarmsStack-1ABCDEF"),
			"CREATE_COMPLETE"),
		stackRes("Pending", "AWS::SQS::Queue", "", "CREATE_FAILED"),
		stackRes("WorkerRole", "AWS::IAM::Role", "billing-worker", "CREATE_COMPLETE"))))
	r.On("cloudformation", "ListStackResources", stackParam("billing"), ok(stackResPage("res-2",
		stackRes("EventsQueue", "AWS::SQS::Queue", queueURL("billing-events"), "UPDATE_COMPLETE"),
		stackRes("OldBucket", "AWS::S3::Bucket", "acme-billing-old", "DELETE_SKIPPED"))))
	r.On("cloudformation", "ListStackResources", stackParam("billing-AlarmsStack-1ABCDEF"), ok(stackResPage("",
		stackRes("AlarmTopic", "AWS::SNS::Topic", "arn:aws:sns:eu-west-1:"+acct+":billing-alarms", "CREATE_COMPLETE"),
		stackRes("Nightly", "AWS::Events::Rule", "billing-nightly", "CREATE_COMPLETE"))))
	r.On("cloudformation", "ListStackResources", stackParam("gone-soon"),
		fail(http.StatusBadRequest, awstest.QueryError("ValidationError", "Stack with id gone-soon does not exist")))
}

func stackLine(r *inventory.StackResource) string {
	line := r.Region + " " + r.Stack + " " + r.LogicalID + " " + r.CFNType + " " + r.PhysicalID
	if r.Root != "" {
		line += " root=" + r.Root
	}
	return line
}

// TestStacks lists stack resources across pages and nested stacks after a throttled call, drops
// deleted, retained and unnamed resources, and records a partial run for the stack that failed
// and a denied run for the region that refused ListStacks.
func TestStacks(t *testing.T) {
	t.Parallel()
	rt := transport(t, "inventory-stacks", stackStubs)
	res, err := inventory.NewCollector(inventory.Options{Env: env(t), Concurrency: 1}).
		Stacks(t.Context(), []inventory.Target{{Account: account(rt), Report: twoRegions()}})
	require.NoError(t, err)

	lines := make([]string, 0, len(res.Resources))
	for i := range res.Resources {
		lines = append(lines, stackLine(&res.Resources[i]))
	}
	assert.ElementsMatch(t, []string{
		"eu-west-1 billing EventsQueue AWS::SQS::Queue " + queueURL("billing-events"),
		"eu-west-1 billing AlarmsStack AWS::CloudFormation::Stack " + stackID("billing-AlarmsStack-1ABCDEF"),
		"eu-west-1 billing WorkerRole AWS::IAM::Role billing-worker",
		"eu-west-1 billing-AlarmsStack-1ABCDEF AlarmTopic AWS::SNS::Topic arn:aws:sns:eu-west-1:" + acct +
			":billing-alarms root=billing",
		"eu-west-1 billing-AlarmsStack-1ABCDEF Nightly AWS::Events::Rule billing-nightly root=billing",
	}, lines)

	require.Len(t, res.Runs, 2)
	byRegion := map[string]inventory.Run{}
	for _, r := range res.Runs {
		assert.Equal(t, inventory.LayerStacks, r.Layer)
		byRegion[r.Region] = r
	}
	assert.Equal(t, inventory.RunPartial, byRegion[euWest].Status)
	assert.Equal(t, 5, byRegion[euWest].Count)
	require.Len(t, byRegion[euWest].Errors, 1)
	assert.Contains(t, byRegion[euWest].Errors[0], "ListStackResources gone-soon")
	assert.Equal(t, inventory.RunDenied, byRegion[usEast].Status)
}

// TestStackIndex joins live resources to stack resources by ID, ARN, name or the ARN's last
// segment, within one account, region and type; account-wide resources join a stack in any region.
func TestStackIndex(t *testing.T) {
	t.Parallel()
	topic := "arn:aws:sns:eu-west-1:" + acct + ":billing-alarms"
	stacks := []inventory.StackResource{
		{
			Account: acct, Region: euWest, CFNType: "AWS::SQS::Queue", PhysicalID: queueURL("billing-events"),
			LogicalID: "EventsQueue", Stack: "billing",
		},
		{
			Account: acct, Region: euWest, CFNType: "AWS::SNS::Topic", PhysicalID: topic, LogicalID: "AlarmTopic",
			Stack: "alarms",
		},
		{
			Account: acct, Region: usEast, CFNType: "AWS::IAM::Role", PhysicalID: "billing-worker",
			LogicalID: "WorkerRole", Stack: "billing-east",
		},
		{
			Account: acct, Region: euWest, CFNType: "AWS::Events::Rule", PhysicalID: "billing-nightly",
			LogicalID: "Nightly", Stack: "alarms",
		},
		{
			Account: acct, Region: euWest, CFNType: "AWS::EC2::SecurityGroup", PhysicalID: "sg-1", LogicalID: "Web",
			Stack: "web",
		},
	}
	ix := inventory.NewStackIndex(stacks)
	for _, tc := range []struct {
		name string
		live inventory.CloudResource
		want string // logical ID, empty for no stack
	}{
		{"by ID", inventory.CloudResource{
			Account: acct, Region: euWest, CFNType: "AWS::SQS::Queue", ID: queueURL("billing-events"),
			ARN: queueARN("billing-events"),
		}, "EventsQueue"},
		{"by ARN", inventory.CloudResource{
			Account: acct, Region: euWest, CFNType: "AWS::SNS::Topic", ARN: topic,
		}, "AlarmTopic"},
		{"account-wide by name, stack in another region", inventory.CloudResource{
			Account: acct, CFNType: "AWS::IAM::Role", ID: "AROAWORKER", Name: "billing-worker",
			ARN: "arn:aws:iam::" + acct + ":role/billing-worker",
		}, "WorkerRole"},
		{"by ARN tail", inventory.CloudResource{
			Account: acct, Region: euWest, CFNType: "AWS::Events::Rule",
			ARN: "arn:aws:events:eu-west-1:" + acct + ":rule/billing-nightly",
		}, "Nightly"},
		{"other type with the same ID", inventory.CloudResource{
			Account: acct, Region: euWest, CFNType: "AWS::EC2::VPC", ID: "sg-1",
		}, ""},
		{"same ID in another region", inventory.CloudResource{
			Account: acct, Region: usEast, CFNType: "AWS::EC2::SecurityGroup", ID: "sg-1",
		}, ""},
		{"same ID in another account", inventory.CloudResource{
			Account: "222222222222", Region: euWest, CFNType: "AWS::EC2::SecurityGroup", ID: "sg-1",
		}, ""},
		{"no CloudFormation type", inventory.CloudResource{
			Account: acct, Region: euWest, SourceType: "ec2:security-group", ID: "sg-1",
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ix.Find(&tc.live)
			if tc.want == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.LogicalID)
		})
	}
	var nilIndex *inventory.StackIndex
	assert.Nil(t, nilIndex.Find(&inventory.CloudResource{CFNType: "AWS::SQS::Queue"}), "a nil index finds nothing")
}
