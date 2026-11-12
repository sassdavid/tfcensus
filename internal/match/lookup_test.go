package match_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

func lookupFleet(t *testing.T) (*match.Matcher, *tfstate.Index) {
	t.Helper()
	role := tfstate.Instance{
		ID: "app", ARN: iamARN("role/app"), Identity: map[string]string{"account_id": acct, "name": "app"},
	}
	random := unitState("app", "random_password", tfstate.Instance{ID: "none"})
	random.State.Resources[0].Provider = tfstate.Provider{Source: "hashicorp/random"}
	randomDup := unitState("db", "random_password", tfstate.Instance{ID: "none"})
	randomDup.State.Resources[0].Provider = tfstate.Provider{Source: "hashicorp/random"}
	states := []tfstate.UnitState{
		unitState("old", "aws_iam_role", role),
		unitState("new", "aws_iam_role", role),
		unitState("net", "aws_security_group", tfstate.Instance{ID: "sg-1", ARN: arnOf("ec2", "security-group/sg-1")}),
		unitState("queue", "aws_sqs_queue_policy", tfstate.Instance{ID: "sg-1"}),
		unitState("thing", "aws_unmapped_thing", tfstate.Instance{ID: "t-1", ARN: arnOf("thing", "thing/t-1")}),
		random, randomDup,
	}
	m := loadMap(t)
	ix := match.Index(m, states)
	return match.New(m, ix), ix
}

func TestLookup(t *testing.T) {
	t.Parallel()

	mt, _ := lookupFleet(t)
	for _, tc := range []struct {
		name     string
		q        match.Query
		category match.Category
		key      tfstate.KeyKind
		units    []string
	}{
		{
			"mapped ARN by identity",
			match.Query{Value: iamARN("role/app")},
			match.CategoryDoubleManaged, tfstate.KeyIdentity,
			[]string{"live/new", "live/old"},
		},
		{
			"bare id in two types is ambiguous",
			match.Query{Value: "sg-1"},
			match.CategoryUndetermined, "",
			[]string{"live/net", "live/queue"},
		},
		{
			"bare id of one type in two states",
			match.Query{Value: "app"},
			match.CategoryDoubleManaged, tfstate.KeyID,
			[]string{"live/new", "live/old"},
		},
		{
			"bare id narrowed by --type",
			match.Query{Value: "sg-1", Types: []string{"aws_security_group"}},
			match.CategoryManaged, tfstate.KeyID,
			[]string{"live/net"},
		},
		{
			"unmapped ARN found by ARN",
			match.Query{Value: arnOf("thing", "thing/t-1")},
			match.CategoryManaged, tfstate.KeyARN,
			[]string{"live/thing"},
		},
		{
			"--type without the type",
			match.Query{Value: iamARN("role/app"), Types: []string{"aws_iam_user"}},
			match.CategoryUnmatched, "", nil,
		},
		{
			"unknown ARN type",
			match.Query{Value: arnOf("thing", "thing/t-2")},
			match.CategoryUndetermined, "", nil,
		},
		{
			"random ids are not AWS ids",
			match.Query{Value: "none"},
			match.CategoryUnmatched, "", nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mt.Lookup(&tc.q)
			assert.Equal(t, tc.category, got.Category, got.Reason)
			assert.Equal(t, tc.key, got.Key)
			var units []string
			for _, r := range got.ManagedBy {
				units = append(units, r.Unit)
			}
			assert.Equal(t, tc.units, units)
			assert.NotEmpty(t, got.Rule)
			assert.NotEmpty(t, got.Reason)
		})
	}
}

// TestDoubleManagedSharesLookup: FL-DOUBLE reads Index's DoubleManaged, and whois reads Lookup.
// Both come from one matcher, so every double-managed key is double-managed in whois too.
func TestDoubleManagedSharesLookup(t *testing.T) {
	t.Parallel()

	mt, ix := lookupFleet(t)
	require.Len(t, ix.DoubleManaged, 1, "the role; sg-1 is in two types and random_password ids are left out")
	for _, dm := range ix.DoubleManaged {
		var q match.Query
		switch dm.Kind {
		case tfstate.KeyIdentity:
			q = match.Query{Value: iamARN("role/app")}
		case tfstate.KeyARN, tfstate.KeyID:
			t.Fatalf("unexpected key kind %s", dm.Kind)
		}
		got := mt.Lookup(&q)
		assert.Equal(t, match.CategoryDoubleManaged, got.Category, dm.Key)
		assert.Equal(t, dm.Refs, got.ManagedBy, dm.Key)
	}
}
