package ownership_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	fixtures  = "../../testdata/ownership"
	goldenDir = "../../testdata/golden/ownership"
)

type packCase struct {
	Pack     string                   `json:"pack"`
	Name     string                   `json:"name"`
	Want     string                   `json:"want"`
	Item     json.RawMessage          `json:"item"`
	Related  []json.RawMessage        `json:"related"`
	Resource *inventory.CloudResource `json:"resource"`
	InState  [][2]string              `json:"in_state"`
}

func loadMap(t *testing.T) *typemap.Map {
	t.Helper()
	m, err := typemap.Load()
	require.NoError(t, err)
	return m
}

// input decodes a case's resource and its related Config items into a rule input.
func (c *packCase) input(t *testing.T, m *typemap.Map) *ownership.Resource {
	t.Helper()
	var r inventory.CloudResource
	if c.Resource != nil {
		r = *c.Resource
	} else {
		var err error
		r, err = inventory.DecodeConfigItem(m, c.Item)
		require.NoError(t, err, c.Name)
	}
	all := []inventory.CloudResource{r}
	for _, raw := range c.Related {
		rel, err := inventory.DecodeConfigItem(m, raw)
		require.NoError(t, err, c.Name)
		all = append(all, rel)
	}
	return ownership.FromCloud(&all[0], m, ownership.NewIndex(all))
}

// TestSpec_ownership_1: every built-in pack has positive and negative fixtures from real-shaped
// AWS Config items.
func TestSpec_ownership_1(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join(fixtures, "packs.json"))
	require.NoError(t, err)
	var f struct {
		Cases []packCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(b, &f))
	m := loadMap(t)
	positive, negative := map[string]int{}, map[string]int{}
	var lines []string
	for i := range f.Cases {
		c := &f.Cases[i]
		inState := map[[2]string]bool{}
		for _, s := range c.InState {
			inState[s] = true
		}
		eng, nerr := ownership.New(ownership.Options{
			InState: func(tf, id string) bool { return inState[[2]string{tf, id}] },
		})
		require.NoError(t, nerr)
		d := eng.Evaluate(c.input(t, m), nil, false)
		want := c.Want
		if want == "" {
			want = ownership.RuleNoMatch
			negative[c.Pack]++
		} else {
			require.True(t, strings.HasPrefix(want, "pack/"+c.Pack+"/"), "%s: want a rule of pack %s", c.Name, c.Pack)
			positive[c.Pack]++
		}
		assert.Equal(t, want, d.Rule, c.Name)
		lines = append(lines, strings.TrimSpace(fmt.Sprintf("%-12s %-45s %s %s", c.Pack, c.Name, d.Rule, d.Category)))
	}
	for _, pack := range ownership.PackOrder() {
		assert.Positive(t, positive[pack], "pack %s has no positive fixture", pack)
		assert.Positive(t, negative[pack], "pack %s has no negative fixture", pack)
	}
	testutil.Golden(t, filepath.Join(goldenDir, "packs.golden"), []byte(strings.Join(lines, "\n")+"\n"))
}

// TestExpiry checks that a rule applies through its expires date (UTC) and stops matching after.
func TestExpiry(t *testing.T) {
	t.Parallel()

	rules := []ownership.Rule{{
		Name: "trial", Category: ownership.CategoryIgnored, Match: `resource.tags.?team.orValue("") == "growth"`,
		Reason: "trial", Expires: "2026-09-30",
	}}
	r := &ownership.Resource{Type: "AWS::S3::Bucket", Tags: map[string]string{"team": "growth"}}
	for _, tc := range []struct {
		now     time.Time
		matched bool
	}{
		{time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC), true},
		{time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), false},
	} {
		eng, err := ownership.New(ownership.Options{Rules: rules, Now: func() time.Time { return tc.now }})
		require.NoError(t, err)
		d := eng.Evaluate(r, nil, true)
		assert.Equal(t, tc.matched, d.Matched, tc.now)
		last := d.Trace[len(d.Trace)-1]
		assert.Equal(t, "user/trial", last.Rule)
		if tc.matched {
			assert.Equal(t, ownership.OutcomeMatch, last.Outcome)
			assert.Empty(t, eng.Expired())
		} else {
			assert.Equal(t, ownership.OutcomeExpired, last.Outcome)
			assert.Equal(t, "last day 2026-09-30", last.Detail)
			assert.Equal(t, []string{"trial"}, eng.Expired())
		}
	}
}

// TestSpec_ownership_4: a CEL compile error names the rule and the column.
func TestSpec_ownership_4(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rule ownership.Rule
		want string
	}{
		{
			ownership.Rule{Name: "typo", Category: "ignored", Match: `resource.nmae == "x"`, Reason: "r"},
			`rule "typo": column 9: undefined field 'nmae'`,
		},
		{
			ownership.Rule{Name: "syntax", Category: "ignored", Match: `resource.name ==`, Reason: "r"},
			`rule "syntax": column 17: Syntax error`,
		},
		{
			ownership.Rule{Name: "not-bool", Category: "ignored", Match: `resource.name`, Reason: "r"},
			`rule "not-bool": match returns string, want bool`,
		},
		{
			ownership.Rule{Name: "category", Category: "managed", Match: `true`, Reason: "r"},
			`rule "category": category "managed": want one of other-iac, aws-managed, derived, ignored`,
		},
		{
			ownership.Rule{Name: "no-reason", Category: "ignored", Match: `true`},
			`rule "no-reason": reason is empty`,
		},
		{
			ownership.Rule{Name: "date", Category: "ignored", Match: `true`, Reason: "r", Expires: "31.12.2026"},
			`rule "date": expires "31.12.2026": want YYYY-MM-DD`,
		},
	}
	var all []ownership.Rule
	for _, tt := range tests {
		_, err := ownership.New(ownership.Options{Rules: []ownership.Rule{tt.rule}})
		require.ErrorIs(t, err, ownership.ErrRule, tt.rule.Name)
		assert.Contains(t, err.Error(), tt.want)
		all = append(all, tt.rule)
	}
	_, err := ownership.New(ownership.Options{Rules: all})
	var re *ownership.RuleError
	require.ErrorAs(t, err, &re)
	assert.Len(t, strings.Split(err.Error(), "\n"), len(tests), "every rule error is reported at once")
}

func TestNeverHide(t *testing.T) {
	t.Parallel()

	rules := []ownership.Rule{{Name: "hide", Category: ownership.CategoryIgnored, Match: `true`, Reason: "r"}}
	user := &ownership.Resource{Type: "AWS::IAM::User", Name: "ops"}
	role := &ownership.Resource{Type: "AWS::IAM::Role", Name: "ops"}
	eng, err := ownership.New(
		ownership.Options{Rules: rules, NeverHide: []ownership.Category{ownership.CategoryIgnored}},
	)
	require.NoError(t, err)
	d := eng.Evaluate(user, nil, true)
	assert.False(t, d.Matched, "an IAM user can't be ignored")
	assert.Equal(t, ownership.OutcomeNeverHide, d.Trace[len(d.Trace)-1].Outcome)
	assert.Equal(t, "user/hide", eng.Evaluate(role, nil, false).Rule, "guards cover IAM users only")

	eng, err = ownership.New(ownership.Options{Rules: rules})
	require.NoError(t, err)
	assert.True(t, eng.Evaluate(user, nil, false).Matched, "without never_hide the rule applies")
}

func TestPacksOff(t *testing.T) {
	t.Parallel()

	vpc := &ownership.Resource{Type: "AWS::EC2::VPC", Attrs: map[string]string{"is_default": "true"}}
	eng, err := ownership.New(ownership.Options{Packs: map[string]bool{ownership.PackAWSDefaults: false}})
	require.NoError(t, err)
	d := eng.Evaluate(vpc, nil, true)
	assert.False(t, d.Matched)
	assert.Equal(t, ownership.OutcomePackOff, d.Trace[0].Outcome)
}

func TestFirstMatchWins(t *testing.T) {
	t.Parallel()

	eng, err := ownership.New(ownership.Options{Rules: []ownership.Rule{
		{Name: "first", Category: ownership.CategoryIgnored, Match: `resource.name == "x"`, Reason: "one"},
		{Name: "second", Category: ownership.CategoryDerived, Match: `resource.name == "x"`, Reason: "two"},
	}})
	require.NoError(t, err)
	d := eng.Evaluate(&ownership.Resource{Name: "x"}, nil, true)
	assert.Equal(t, "user/first", d.Rule)
	assert.Equal(t, ownership.OutcomeSkipped, d.Trace[len(d.Trace)-1].Outcome)
	assert.Len(t, d.Trace, len(eng.Rules()))
	ids := eng.Rules()
	assert.True(t, strings.HasPrefix(ids[0], "pack/"+ownership.PackAWSDefaults+"/"), "packs run first")
	assert.Equal(t, []string{"user/first", "user/second"}, ids[len(ids)-2:], "user rules run last, in order")
	_, err = ownership.New(ownership.Options{Rules: []ownership.Rule{
		{Name: "dup", Category: ownership.CategoryIgnored, Match: `true`, Reason: "r"},
		{Name: "dup", Category: ownership.CategoryIgnored, Match: `true`, Reason: "r"},
	}})
	assert.ErrorIs(t, err, ownership.ErrRule)
}
