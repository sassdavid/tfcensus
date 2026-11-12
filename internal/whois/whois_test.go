package whois_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
	"github.com/sassdavid/tfcensus/internal/whois"
)

const acct = "111111111111"

func resolver(t *testing.T) *whois.Resolver {
	t.Helper()
	m, err := typemap.Load()
	require.NoError(t, err)
	eng, err := ownership.New(ownership.Options{})
	require.NoError(t, err)
	unit := func(path, tf string, inst tfstate.Instance) tfstate.UnitState {
		return tfstate.UnitState{
			Repo: "live", Path: path, Workspace: tfstate.DefaultWorkspace, Status: tfstate.StatusOK,
			Target: discovery.Target{Account: acct, Region: "eu-west-1"},
			State: &tfstate.State{Version: 4, Resources: []tfstate.Resource{{
				Mode: tfstate.ModeManaged, Type: tf, Name: "this",
				Provider: tfstate.Provider{Source: "hashicorp/aws"}, Instances: []tfstate.Instance{inst},
			}}},
		}
	}
	ix := match.Index(m, []tfstate.UnitState{
		unit("iam", "aws_iam_role", tfstate.Instance{ID: "app", ARN: "arn:aws:iam::" + acct + ":role/app"}),
		unit("net", "aws_security_group", tfstate.Instance{ID: "x-1"}),
		unit("q", "aws_sqs_queue_policy", tfstate.Instance{ID: "x-1"}),
	})
	return &whois.Resolver{Matcher: match.New(m, ix), Map: m, Engine: eng}
}

func TestResolveAndRefine(t *testing.T) {
	t.Parallel()

	rv := resolver(t)
	q := match.Query{Value: "arn:aws:iam::" + acct + ":role/app"}
	a := rv.Resolve(&q)
	assert.Equal(t, scan.CategoryManaged, a.Finding.Category)
	assert.Equal(t, whois.LiveNotNeeded, a.Live)
	assert.False(t, rv.NeedsLive(&a))
	require.Len(t, a.ManagedBy, 1)
	assert.Equal(t, whois.Manager{
		Unit: "live/iam", Workspace: "default", Address: "aws_iam_role.this", Type: "aws_iam_role", Account: acct,
	}, a.ManagedBy[0])

	q = match.Query{Value: "arn:aws:iam::" + acct + ":role/stack"}
	a = rv.Resolve(&q)
	assert.Equal(t, scan.CategoryUnmanaged, a.Finding.Category)
	assert.Equal(t, whois.LiveOffline, a.Live)
	require.True(t, rv.NeedsLive(&a))

	notListed := a
	rv.Refine(&notListed, &q, nil, false, nil)
	assert.Equal(t, whois.LiveNotListed, notListed.Live)
	assert.Equal(t, scan.CategoryUnmanaged, notListed.Finding.Category, "an unlisted type keeps the offline verdict")

	gone := a
	rv.Refine(&gone, &q, nil, true, nil)
	assert.Equal(t, whois.LiveNotFound, gone.Live)
	assert.Equal(t, scan.CategoryUndetermined, gone.Finding.Category)
	assert.Equal(t, whois.RuleNotFound, gone.Finding.Rule)

	live := inventory.CloudResource{
		Account: acct, CFNType: "AWS::IAM::Role", ARN: q.Value, ID: "AROA1", Name: "stack",
		Tags: map[string]string{"aws:cloudformation:stack-name": "s"}, Attrs: map[string]string{},
	}
	found := a
	rv.Refine(&found, &q, &live, true, ownership.NewIndex([]inventory.CloudResource{live}))
	assert.Equal(t, whois.LiveFound, found.Live)
	assert.Equal(t, scan.CategoryOtherIaC, found.Finding.Category)
	assert.Equal(t, "cloudformation", found.Finding.Owner)
}

func TestAmbiguousSkipsRules(t *testing.T) {
	t.Parallel()

	rv := resolver(t)
	a := rv.Resolve(&match.Query{Value: "x-1"})
	assert.Equal(t, scan.CategoryUndetermined, a.Finding.Category)
	assert.Equal(t, match.RuleAmbiguous, a.Finding.Rule)
	assert.Len(t, a.ManagedBy, 2)
	assert.Equal(t, whois.LiveNotNeeded, a.Live)
	assert.False(t, rv.NeedsLive(&a))
}

func TestListed(t *testing.T) {
	t.Parallel()

	cov := &inventory.Coverage{
		Columns: []inventory.Column{
			{Account: acct, Region: inventory.GlobalColumn}, {Account: acct, Region: "eu-west-1"},
		},
		Rows: []inventory.CoverageRow{
			{CFNType: "AWS::IAM::Role", Global: true, Cells: [][]string{{"config"}, {}}},
			{CFNType: "AWS::EC2::SecurityGroup", Cells: [][]string{{}, {"config!"}}},
		},
	}
	assert.True(t, whois.Listed(cov, acct, "", "AWS::IAM::Role"))
	assert.False(t, whois.Listed(cov, acct, "eu-west-1", "AWS::EC2::SecurityGroup"), "a failed layer")
	assert.False(t, whois.Listed(cov, "222222222222", "", "AWS::IAM::Role"))
	assert.False(t, whois.Listed(nil, acct, "", "AWS::IAM::Role"))
}
