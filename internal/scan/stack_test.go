package scan_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	stackAcct = "111111111111"
	stackRgn  = "eu-west-1"
)

func queue(name string) inventory.CloudResource {
	return inventory.CloudResource{
		Account: stackAcct, Region: stackRgn, CFNType: "AWS::SQS::Queue",
		ARN:  "arn:aws:sqs:eu-west-1:" + stackAcct + ":" + name,
		ID:   "https://sqs.eu-west-1.amazonaws.com/" + stackAcct + "/" + name,
		Tags: map[string]string{"aws:cloudformation:stack-name": "billing"}, Sources: []string{inventory.LayerConfig},
	}
}

// stackSources returns classification sources with every pack on, a user rule that ignores
// everything, and stack resources for the billing-events queue, the nested stack's events rule
// and the acme-api function.
func stackSources(t *testing.T) scan.Sources {
	t.Helper()
	m, err := typemap.Load()
	require.NoError(t, err)
	eng, err := ownership.New(ownership.Options{Rules: []ownership.Rule{{
		Name: "ignore-all", Category: ownership.CategoryIgnored, Match: "true", Reason: "test",
	}}})
	require.NoError(t, err)
	stacks := []inventory.StackResource{
		{
			Account: stackAcct, Region: stackRgn, CFNType: "AWS::SQS::Queue", PhysicalID: queue("billing-events").ID,
			LogicalID: "EventsQueue", Stack: "billing", StackID: "billing-id",
		},
		{
			Account: stackAcct, Region: stackRgn, CFNType: "AWS::Events::Rule", PhysicalID: "billing-nightly",
			LogicalID: "Nightly", Stack: "billing-AlarmsStack-1ABCDEF", StackID: "alarms-id", Root: "billing",
		},
		{
			Account: stackAcct, Region: stackRgn, CFNType: "AWS::Lambda::Function", PhysicalID: "acme-api",
			LogicalID: "Api", Stack: "billing", StackID: "billing-id",
		},
	}
	return scan.Sources{Engine: eng, Map: m, Stacks: inventory.NewStackIndex(stacks)}
}

// TestClassifyStacks: a state match decides first and keeps the stack as evidence; an unmatched
// or undetermined resource that a stack lists is other-iac before any rule runs; the rest go to
// the rules.
func TestClassifyStacks(t *testing.T) {
	t.Parallel()
	src := stackSources(t)
	rule := inventory.CloudResource{
		Account: stackAcct, Region: stackRgn, CFNType: "AWS::Events::Rule",
		ARN: "arn:aws:events:eu-west-1:" + stackAcct + ":rule/billing-nightly", Sources: []string{inventory.LayerRE},
	}
	fn := inventory.CloudResource{
		Account: stackAcct, Region: stackRgn, CFNType: "AWS::Lambda::Function", ID: "acme-api", Name: "acme-api",
		ARN: "arn:aws:lambda:eu-west-1:" + stackAcct + ":function:acme-api", Sources: []string{inventory.LayerConfig},
	}
	results := []match.Result{
		{Resource: queue("billing-events"), Category: match.CategoryUnmatched, Rule: match.RuleNotInState, Reason: "x"},
		{Resource: rule, Category: match.CategoryUndetermined, Rule: match.RuleNoMapping, Reason: "y"},
		{
			Resource: fn, Category: match.CategoryManaged, Rule: match.RuleIdentity, Reason: "z",
			ManagedBy: []tfstate.Ref{{Unit: "tf-scan/api", Address: "aws_lambda_function.api"}},
		},
		{Resource: queue("other"), Category: match.CategoryUnmatched, Rule: match.RuleNotInState, Reason: "x"},
	}
	got := scan.Classify(results, src)
	require.Len(t, got, 4)

	q := got[0]
	assert.Equal(t, scan.CategoryOtherIaC, q.Category)
	assert.Equal(t, scan.RuleCloudFormation, q.Rule, "the stack decides before the tag pack and the user rule")
	assert.Equal(t, scan.OwnerCloudFormation, q.Owner)
	assert.Equal(t, "listed by CloudFormation stack billing as EventsQueue", q.Reason)
	assert.Equal(t, &scan.Stack{Name: "billing", ID: "billing-id", LogicalID: "EventsQueue"}, q.Stack)

	r := got[1]
	assert.Equal(t, scan.CategoryOtherIaC, r.Category, "an unmapped type in a stack is other-iac")
	assert.Equal(t, "listed by CloudFormation stack billing-AlarmsStack-1ABCDEF (nested in billing) as Nightly",
		r.Reason)

	f := got[2]
	assert.Equal(t, scan.CategoryManaged, f.Category, "the state wins")
	assert.Equal(t, match.RuleIdentity, f.Rule)
	require.NotNil(t, f.Stack)
	assert.Equal(t, "Api", f.Stack.LogicalID)
	assert.Equal(t, []string{"AWS::Lambda::Function arn:aws:lambda:eu-west-1:111111111111:function:acme-api is in a " +
		"state and in CloudFormation stack billing as Api"}, scan.StackOverlaps(got))

	o := got[3]
	assert.Equal(t, "pack/other-iac/cloudformation", o.Rule, "a resource no stack lists goes to the rules")
	assert.Nil(t, o.Stack)

	src.Stacks = nil
	off := scan.Classify(results[:2], src)
	assert.Equal(t, "pack/other-iac/cloudformation", off[0].Rule, "without the stack source the tag rule decides")
	assert.Nil(t, off[0].Stack)
	assert.Equal(t, scan.CategoryIgnored, off[1].Category, "an untagged resource falls through to the user rule")
}

// TestExplainStack: --explain names the stack and skips the rules it decided.
func TestExplainStack(t *testing.T) {
	t.Parallel()
	src := stackSources(t)
	res := match.Result{
		Resource: queue("billing-events"), Category: match.CategoryUnmatched, Rule: match.RuleNotInState,
		Reason: "no state has it",
	}
	ex := scan.Explain([]match.Result{res}, 0, src)
	assert.Empty(t, ex.Trace)
	var b bytes.Buffer
	require.NoError(t, scan.WriteExplanation(&b, &ex))
	assert.Contains(t, b.String(), "stack     CloudFormation stack billing as EventsQueue\n")
	assert.Contains(t, b.String(), "rules     not evaluated: a CloudFormation stack lists it\n")
	assert.Contains(t, b.String(), "category  other-iac, owner cloudformation: listed by CloudFormation stack billing "+
		"as EventsQueue (source/cloudformation)\n")
}
