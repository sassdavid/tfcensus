package suggest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/migrate"
	"github.com/sassdavid/tfcensus/internal/suggest"
	"github.com/sassdavid/tfcensus/internal/testutil"
)

const plans = "../../testdata/move/suggest/plans"

func readPlan(t *testing.T, unit string) *tfjson.Plan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(plans, unit, "tfplan.json"))
	require.NoError(t, err)
	var p tfjson.Plan
	require.NoError(t, json.Unmarshal(b, &p))
	return &p
}

func fixtureUnits(t *testing.T) []suggest.Unit {
	t.Helper()
	return []suggest.Unit{
		{Ref: "live/network", From: true, To: true, Plan: readPlan(t, "live/network")},
		{Ref: "live/network-sg", To: true, Plan: readPlan(t, "live/network-sg")},
		{Ref: "live/network-endpoints", To: true, Plan: readPlan(t, "live/network-endpoints")},
	}
}

func rules(t *testing.T, ss ...string) []suggest.Rule {
	t.Helper()
	out := make([]suggest.Rule, 0, len(ss))
	for _, s := range ss {
		r, err := suggest.ParseRule(s)
		require.NoError(t, err)
		out = append(out, r)
	}
	return out
}

func moveKeys(res *suggest.Result) []string {
	out := make([]string, 0, len(res.Moves))
	for _, m := range res.Moves {
		out = append(out, m.FromUnit+" "+m.FromAddress+" -> "+m.ToUnit+" "+m.ToAddress)
	}
	return out
}

// TestMatchFixture runs the engine over the split-network fixture, without and with a json rule
// for the endpoint policy.
func TestMatchFixture(t *testing.T) {
	t.Parallel()
	base := []string{
		"live/network aws_security_group.app -> live/network-sg aws_security_group.app",
		"live/network aws_ssm_parameter.db_password -> live/network-sg aws_ssm_parameter.db_password",
		"live/network aws_vpc.main -> live/network aws_vpc.this",
		`live/network aws_vpc_security_group_ingress_rule.app["http"] -> ` +
			`live/network-sg aws_vpc_security_group_ingress_rule.app["http"]`,
		`live/network aws_vpc_security_group_ingress_rule.app["https"] -> ` +
			`live/network-sg aws_vpc_security_group_ingress_rule.app["https"]`,
	}
	endpoint := `live/network aws_vpc_endpoint.s3 -> live/network-endpoints module.endpoints.aws_vpc_endpoint.this["s3"]`
	tests := []struct {
		name  string
		rules []string
		want  []string
	}{
		{"strict", nil, base},
		{"json policy", []string{"json:aws_vpc_endpoint:policy"}, append(append([]string{}, base[:2]...),
			append([]string{endpoint}, base[2:]...)...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := suggest.Match(fixtureUnits(t), rules(t, tt.rules...))
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want, moveKeys(res))
		})
	}
}

// TestMatchUnmatched checks the reasons the strict run reports for what it didn't move.
func TestMatchUnmatched(t *testing.T) {
	t.Parallel()
	res, err := suggest.Match(fixtureUnits(t), nil)
	require.NoError(t, err)
	byAddr := map[string]suggest.Unmatched{}
	for _, u := range res.Unmatched {
		byAddr[u.Action+" "+u.Address] = u
	}

	assoc := byAddr["delete aws_vpc_endpoint_route_table_association.s3"]
	assert.Len(t, assoc.Candidates, 2, "two identical creates make the delete ambiguous")
	assert.Nil(t, assoc.Closest)
	assert.Len(t, byAddr[`create module.endpoints.aws_vpc_endpoint_route_table_association.this["a"]`].Candidates, 1)

	ep := byAddr["delete aws_vpc_endpoint.s3"]
	require.NotNil(t, ep.Closest)
	assert.Equal(t, []string{"policy"}, ep.Closest.Differ)

	extra := byAddr["create aws_security_group.extra"]
	require.NotNil(t, extra.Closest)
	assert.Equal(t, "aws_security_group.app", extra.Closest.Address)
	assert.Equal(t, []string{"name", "tags"}, extra.Closest.Differ)

	legacy := byAddr["delete aws_iam_role.legacy"]
	assert.Empty(t, legacy.Candidates)
	assert.Nil(t, legacy.Closest)

	for k := range byAddr {
		assert.NotContains(t, k, "aws_route.", "replaced resources never take part")
		assert.NotContains(t, k, "aws_subnet.", "updates never take part")
		assert.NotContains(t, k, "data.", "data sources never take part")
	}
}

func change(addr string, actions []string, before, after, unknown any) *tfjson.ResourceChange {
	acts := make(tfjson.Actions, 0, len(actions))
	for _, a := range actions {
		acts = append(acts, tfjson.Action(a))
	}
	return &tfjson.ResourceChange{
		Address: addr, Mode: tfjson.ManagedResourceMode, Type: "t",
		Change: &tfjson.Change{Actions: acts, Before: before, After: after, AfterUnknown: unknown},
	}
}

func obj(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// TestMatchCompare covers one attribute comparison per case: a delete in unit a against a create
// in unit b.
func TestMatchCompare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		before  map[string]any
		after   map[string]any
		unknown map[string]any
		rules   []string
		match   bool
	}{
		{"equal", obj("name", "x", "n", 1.0), obj("name", "x", "n", 1.0), nil, nil, true},
		{"different", obj("name", "x"), obj("name", "y"), nil, nil, false},
		{"unknown after apply is skipped", obj("name", "x", "id", "i-1"), obj("name", "x"), obj("id", true), nil, true},
		{
			"unknown parent covers children", obj("tags_all", obj("Name", "x")), obj(), obj("tags_all", true),
			nil, true,
		},
		{"dropped attribute differs", obj("name", "x", "user_data", "boot"), obj("name", "x"), nil, nil, false},
		{"null equals empty", obj("a", "", "b", []any{}, "c", map[string]any{}), obj("a", nil), nil, nil, true},
		{"list length differs", obj("l", []any{"a", "b"}), obj("l", []any{"a"}), nil, nil, false},
		{"everything", obj("length", 3.0), obj("length", 4.0), nil, []string{"everything:t:length"}, true},
		{"rule for another type", obj("length", 3.0), obj("length", 4.0), nil, []string{"everything:u:length"}, false},
		{"any type", obj("length", 3.0), obj("length", 4.0), nil, []string{"everything:*:length"}, true},
		{"whitespace", obj("p", "a b\n"), obj("p", "ab"), nil, []string{"whitespace:t:p"}, true},
		{"prefix", obj("bucket", "b/x"), obj("bucket", "x"), nil, []string{"prefix:t:bucket:b/"}, true},
		{"json", obj("p", `{"a":1,"b":[2]}`), obj("p", "{\n \"b\": [2], \"a\": 1}"), nil, []string{"json:t:p"}, true},
		{"json still compares", obj("p", `{"a":1}`), obj("p", `{"a":2}`), nil, []string{"json:t:p"}, false},
		{
			"rule covers keys below", obj("tags", obj("A", "1")), obj("tags", obj("A", "2", "B", "3")), nil,
			[]string{"everything:t:tags"},
			true,
		},
		{
			"wildcard segment", obj("tags", obj("A", "1")), obj("tags", obj("A", "2")), nil,
			[]string{"everything:t:tags.*"},
			true,
		},
		{
			"tfautomv count key", obj("l", []any{"a"}), obj("l", []any{"a", "b"}), nil,
			[]string{"everything:t:l.#", "everything:t:l.1"},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var unknown any = tt.unknown
			if tt.unknown == nil {
				unknown = map[string]any{}
			}
			units := []suggest.Unit{
				{Ref: "a", From: true, Plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
					change("t.old", []string{"delete"}, tt.before, nil, false),
				}}},
				{Ref: "b", To: true, Plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
					change("t.new", []string{"create"}, nil, tt.after, unknown),
				}}},
			}
			res, err := suggest.Match(units, rules(t, tt.rules...))
			require.NoError(t, err)
			assert.Equal(t, 1, res.Comparisons)
			assert.Equal(t, tt.match, len(res.Moves) == 1)
		})
	}
}

// TestMatchSides checks that deletes come only from From units and creates only from To units.
func TestMatchSides(t *testing.T) {
	t.Parallel()
	v := obj("name", "x")
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		change("t.old", []string{"delete"}, v, nil, false),
		change("t.new", []string{"create"}, nil, v, map[string]any{}),
	}}
	tests := []struct {
		name     string
		from, to bool
		moves    int
	}{
		{"both", true, true, 1},
		{"from only", true, false, 0},
		{"to only", false, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := suggest.Match([]suggest.Unit{{Ref: "a", From: tt.from, To: tt.to, Plan: p}}, nil)
			require.NoError(t, err)
			assert.Len(t, res.Moves, tt.moves)
		})
	}
}

func TestMatchNotObject(t *testing.T) {
	t.Parallel()
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		change("t.old", []string{"delete"}, "FIXTURE-SECRET", nil, false),
	}}
	_, err := suggest.Match([]suggest.Unit{{Ref: "a", From: true, Plan: p}}, nil)
	require.ErrorContains(t, err, "t.old is not an object")
	assert.NotContains(t, err.Error(), "FIXTURE-SECRET")
}

func TestParseRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
		err  bool
	}{
		{"everything:aws_instance:credit_specification.#", "everything:aws_instance:credit_specification.#", false},
		{
			"prefix:google_storage_bucket_iam_member:bucket:b/",
			"prefix:google_storage_bucket_iam_member:bucket:b/",
			false,
		},
		{"prefix:t:a:x:y", "prefix:t:a:x:y", false},
		{"json:*:policy", "json:*:policy", false},
		{"whitespace:t", "", true},
		{"everything:t:a:b", "", true},
		{"prefix:t:a", "", true},
		{"prefix:t:a:", "", true},
		{"regex:t:a", "", true},
		{"everything::a", "", true},
		{"everything:t:a..b", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			r, err := suggest.ParseRule(tt.in)
			if tt.err {
				require.ErrorIs(t, err, suggest.ErrRule)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, r.String())
		})
	}
}

// TestDraft checks the drafted file against a golden and parses it back with the migration file
// parser, so every drafted block is one move plan accepts.
func TestDraft(t *testing.T) {
	t.Parallel()
	res, err := suggest.Match(fixtureUnits(t), rules(t, "json:aws_vpc_endpoint:policy"))
	require.NoError(t, err)
	draft := suggest.Draft(res.Moves)
	testutil.Golden(t, "../../testdata/golden/move/suggest/draft.hcl", draft)

	f, err := migrate.Parse(draft, "draft.hcl", nil, nil)
	require.NoError(t, err)
	require.Len(t, f.Blocks, 3)
	assert.Equal(t, "live/network", f.Blocks[0].From)
	assert.Len(t, f.Blocks[0].To, 2)
	assert.Equal(t, []string{"mv aws_vpc.main aws_vpc.this"}, f.Blocks[1].Actions)
	assert.Equal(t, "live/network-endpoints", f.Blocks[2].From)
	assert.Equal(t, []string{`mv aws_vpc_endpoint.s3 'module.endpoints.aws_vpc_endpoint.this["s3"]'`},
		f.Blocks[2].Actions)

	assert.Nil(t, suggest.Draft(nil))
}

// TestDraftQuoting checks addresses whose instance keys need quoting in an action.
func TestDraftQuoting(t *testing.T) {
	t.Parallel()
	moves := []suggest.Move{
		{Type: "t", FromUnit: "u", FromAddress: `t.a["it's"]`, ToUnit: "u", ToAddress: `t.b["a \"q\" ${x}"]`},
		{Type: "t", FromUnit: "u", FromAddress: `t.c[0]`, ToUnit: "u", ToAddress: `t.d["x y"]`},
	}
	f, err := migrate.Parse(suggest.Draft(moves), "draft.hcl", nil, nil)
	require.NoError(t, err)
	require.Len(t, f.Blocks, 1)
	assert.Equal(t, []string{
		`mv "t.a[\"it's\"]" 't.b["a \"q\" ${x}"]'`,
		`mv t.c[0] 't.d["x y"]'`,
	}, f.Blocks[0].Actions)
}
