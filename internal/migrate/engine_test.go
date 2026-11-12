package migrate_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/migrate"
	"github.com/sassdavid/tfcensus/internal/prompt"
	"github.com/sassdavid/tfcensus/internal/testutil"
)

func yes() prompt.Gate { return prompt.Gate{Yes: true} }

// TestSpec_move_3: verification rejects a move whose destination provider targets the wrong
// account: refreshing the moved security group there finds nothing, so the plan creates it. The
// ARN precheck catches the same mistake before planning when discovery knows the account.
func TestSpec_move_3(t *testing.T) {
	t.Parallel()
	t.Run("plan", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, unitNetwork, unitSG, unitEndpoints)
		r.seed(unitNetwork, "network.tfstate")
		r.seed(unitEndpoints, "network-endpoints.tfstate")
		r.units[unitSG].Account = "" // the provider's account isn't known to discovery
		r.planner.plans[r.units[unitSG].Dir] = plan(t, "wrong-account.json")
		f := parseFile(t, splitFile, nil)

		rep, err := r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
		require.ErrorIs(t, err, migrate.ErrRejected)
		br := rep.Blocks[0]
		assert.Equal(t, migrate.ResultRejected, br.Result)
		assert.Equal(t, []migrate.Finding{{
			Rule: migrate.RuleVerify, Unit: unitSG, Address: "aws_security_group.app",
			Reason: "the plan would create it; a move may only leave no-op and read",
		}}, br.Findings)
		assert.Empty(t, r.pusher.calls)
		assert.Empty(t, r.s3.writes)
	})
	t.Run("precheck", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, unitNetwork, unitSG, unitEndpoints)
		r.seed(unitNetwork, "network.tfstate")
		r.seed(unitEndpoints, "network-endpoints.tfstate")
		r.units[unitSG].Account = "222222222222"
		f := parseFile(t, splitFile, nil)

		rep, err := r.engine(false).Plan(t.Context(), f, f.Blocks)
		require.NoError(t, err)
		var rules []string
		for _, fd := range rep.Blocks[0].Findings {
			rules = append(rules, fd.Rule+" "+fd.Address)
		}
		assert.Equal(t, []string{
			"MV-ACCOUNT aws_security_group.app",
			`MV-ACCOUNT aws_vpc_security_group_ingress_rule.app["https"]`,
			`MV-ACCOUNT aws_vpc_security_group_ingress_rule.app["ssh"]`,
		}, rules)
		assert.Equal(t, migrate.ResultRejected, rep.Blocks[0].Result)
	})
}

// TestSpec_move_4: history lives in each unit, so one migration file applies to env1 and env2
// independently (tfmigrate #200).
func TestSpec_move_4(t *testing.T) {
	t.Parallel()
	r := newRig(t, "live/env1/network", "live/env1/network-sg", "live/env2/network", "live/env2/network-sg")
	r.seed("live/env1/network", "network.tfstate")
	r.seed("live/env2/network", "network.tfstate")
	src := `
migration "units" "split_sg" {
  from = "live/${var.env}/network"
  to   = { "live/${var.env}/network-sg" = ["aws_security_group.app"] }
}
`
	env := func(name string) *migrate.File { return parseFile(t, src, map[string]string{"env": name}) }
	status := func(f *migrate.File) string {
		rep, err := r.engine(false).Status(t.Context(), f, f.Blocks)
		require.NoError(t, err)
		return rep.Blocks[0].Status
	}

	f1 := env("env1")
	_, err := r.engine(true).Apply(t.Context(), f1, f1.Blocks, yes(), false)
	require.NoError(t, err)
	assert.Equal(t, migrate.StatusApplied, status(f1))
	assert.Equal(t, migrate.StatusPending, status(env("env2")))

	f2 := env("env2")
	rep, err := r.engine(true).Apply(t.Context(), f2, f2.Blocks, yes(), false)
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultApplied, rep.Blocks[0].Result)
	assert.Equal(t, migrate.StatusApplied, status(f2))

	rep, err = r.engine(true).Apply(t.Context(), f1, f1.Blocks, yes(), false)
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultSkipped, rep.Blocks[0].Result)
	assert.Equal(t, []string{
		"live/env1/network-sg", "live/env1/network", "live/env2/network-sg", "live/env2/network",
	}, r.pusher.calls, "env1 is pushed once")

	for _, p := range []string{"live/env1/network", "live/env2/network-sg"} {
		h := r.history(p)
		require.NotNil(t, h, p)
		require.Len(t, h.Records, 1, p)
		assert.Equal(t, "split.hcl", h.Records[0].File)
		assert.Equal(t, "split_sg", h.Records[0].Block)
		assert.Equal(t, testBy, h.Records[0].By)
		assert.Equal(t, p, h.Unit)
	}
}

// TestApplyRollback applies a split, then rolls it back: every state is byte-identical to the
// original, the new unit's state is gone again, and the DynamoDB digest matches the restored bytes.
func TestApplyRollback(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	srcOrig := r.seed(unitNetwork, "network.tfstate")
	endOrig := r.seed(unitEndpoints, "network-endpoints.tfstate")
	r.units[unitNetwork].Table, r.units[unitNetwork].Lock = testTable, discovery.LockBoth
	f := parseFile(t, splitFile, nil)
	prompter := &fakePrompter{}

	rep, err := r.engine(true).Apply(t.Context(), f, f.Blocks, prompt.Gate{Prompter: prompter}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{unitEndpoints}, prompter.asked, "type the last destination")
	assert.Equal(t, []string{unitSG, unitEndpoints, unitNetwork}, r.pusher.calls, "destinations before the source")
	assert.Equal(t, migrate.ResultApplied, rep.Blocks[0].Result)
	assert.Equal(
		t,
		&migrate.VersionIDs{After: rep.Blocks[0].Units[0].VersionIDs.After},
		rep.Blocks[0].Units[0].VersionIDs,
		"the new unit had no state before",
	)
	srcNow, _ := r.s3.body(t, r.units[unitNetwork].Key)
	assert.NotEqual(t, srcOrig, srcNow)
	rec := r.history(unitNetwork).Records[0]
	assert.Equal(t, migrate.RoleSource, rec.Role)
	assert.NotEmpty(t, rec.VersionIDs.Before)

	rep, err = r.engine(true).Rollback(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultRolledBack, rep.Blocks[0].Result)
	got, ok := r.s3.body(t, r.units[unitNetwork].Key)
	require.True(t, ok)
	assert.Equal(t, srcOrig, got, "source restored byte for byte")
	got, ok = r.s3.body(t, r.units[unitEndpoints].Key)
	require.True(t, ok)
	assert.Equal(t, endOrig, got, "destination restored byte for byte")
	_, ok = r.s3.body(t, r.units[unitSG].Key)
	assert.False(t, ok, "the new unit's state is deleted again")

	sum := md5.Sum(srcOrig)
	digestID := testBucket + "/" + r.units[unitNetwork].Key + "-md5"
	assert.Equal(t, map[string]map[string]string{
		digestID: {"LockID": digestID, "Digest": hex.EncodeToString(sum[:])},
	}, r.table.items, "only the digest is left; the lock is released")
	for _, p := range []string{unitNetwork, unitSG, unitEndpoints} {
		lockKey := r.units[p].Key + ".tflock"
		assert.Contains(t, r.s3.writes, "PutObject "+testBucket+"/"+lockKey, "rollback takes the lock")
		_, held := r.s3.body(t, lockKey)
		assert.False(t, held, "and releases it")
	}
	assert.NotNil(t, r.history(unitNetwork).Records[0].RolledBackAt)

	st, err := r.engine(false).Status(t.Context(), f, f.Blocks)
	require.NoError(t, err)
	assert.Equal(t, migrate.StatusPending, st.Blocks[0].Status)

	// Rolled back, the block can be applied again.
	_, err = r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
}

func TestApplyStopsWhenStateChanged(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	orig := r.seed(unitNetwork, "network.tfstate")
	r.seed(unitEndpoints, "network-endpoints.tfstate")
	// Someone applies the source while tfcensus is verifying.
	r.planner.hook = func(dir string) {
		if dir == r.units[unitNetwork].Dir {
			r.s3.put(r.units[unitNetwork].Key, orig)
		}
	}
	f := parseFile(t, splitFile, nil)
	_, err := r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.ErrorIs(t, err, migrate.ErrChanged)
	assert.Empty(t, r.pusher.calls)
}

func TestRollbackStopsWhenStateChanged(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG)
	orig := r.seed(unitNetwork, "network.tfstate")
	f := parseFile(t, `migration "units" "x" {
  from = "live/network"
  to   = { "live/network-sg" = ["aws_security_group.app"] }
}`, nil)
	_, err := r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	later, _ := r.s3.body(t, r.units[unitSG].Key)
	r.s3.put(r.units[unitSG].Key, later)

	_, err = r.engine(true).Rollback(t.Context(), f, f.Blocks, yes(), false)
	require.ErrorIs(t, err, migrate.ErrChanged)
	got, _ := r.s3.body(t, r.units[unitNetwork].Key)
	assert.NotEqual(t, orig, got, "nothing restored")

	_, err = r.engine(true).Rollback(t.Context(), f, f.Blocks, yes(), true)
	require.NoError(t, err)
	got, _ = r.s3.body(t, r.units[unitNetwork].Key)
	assert.Equal(t, orig, got)
}

func TestApplyGate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		gate prompt.Gate
		want error
	}{
		{"no terminal and no --yes", prompt.Gate{}, prompt.ErrNoInput},
		{"wrong answer", prompt.Gate{Prompter: &fakePrompter{err: prompt.ErrDeclined}}, prompt.ErrDeclined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, unitNetwork, unitSG, unitEndpoints)
			r.seed(unitNetwork, "network.tfstate")
			f := parseFile(t, splitFile, nil)
			_, err := r.engine(true).Apply(t.Context(), f, f.Blocks, tt.gate, false)
			require.ErrorIs(t, err, tt.want)
			assert.Empty(t, r.pusher.calls)
			assert.Empty(t, r.s3.writes)
		})
	}
}

func TestMarkApplied(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	r.seed(unitNetwork, "network.tfstate")
	f := parseFile(t, splitFile, nil)
	prompter := &fakePrompter{}
	rep, err := r.engine(true).MarkApplied(t.Context(), f, f.Blocks, prompt.Gate{Prompter: prompter})
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultMarked, rep.Blocks[0].Result)
	assert.Equal(t, []string{unitEndpoints}, prompter.asked)
	for _, p := range []string{unitNetwork, unitSG, unitEndpoints} {
		h := r.history(p)
		require.Len(t, h.Records, 1)
		assert.True(t, h.Records[0].Manual)
		assert.Nil(t, h.Records[0].VersionIDs)
	}
	_, err = r.engine(true).Rollback(t.Context(), f, f.Blocks, yes(), false)
	require.ErrorIs(t, err, migrate.ErrManual)
	rep, err = r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultSkipped, rep.Blocks[0].Result)
	assert.Empty(t, r.pusher.calls)
}

func TestPartialBlockStopsApply(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	r.seed(unitNetwork, "network.tfstate")
	r.seed(unitEndpoints, "network-endpoints.tfstate")
	f := parseFile(t, splitFile, nil)
	// The source push fails after both destinations were pushed.
	failing := &failingPusher{fakePusher: r.pusher, fail: r.units[unitNetwork].Dir}
	e, err := migrate.New(migrate.Options{
		Units: r.units, Planner: r.planner, Pusher: failing,
		Clients: func(_ context.Context, _ *migrate.Unit) (*migrate.Clients, error) {
			return &migrate.Clients{Reader: r.s3, Writer: r.s3, By: testBy}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = e.Close() })
	rep, err := e.Apply(t.Context(), f, f.Blocks, yes(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already pushed: live/network-sg, live/network-endpoints")
	assert.Equal(t, migrate.ResultPartial, rep.Blocks[0].Result)

	st, err := r.engine(false).Status(t.Context(), f, f.Blocks)
	require.NoError(t, err)
	assert.Equal(t, migrate.StatusPartial, st.Blocks[0].Status)
	_, err = r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.ErrorIs(t, err, migrate.ErrPartial)
	_, err = r.engine(true).Rollback(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	_, ok := r.s3.body(t, r.units[unitSG].Key)
	assert.False(t, ok)
}

func TestVersioningRequired(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	r.s3.unversion = true
	r.seed(unitNetwork, "network.tfstate")
	f := parseFile(t, splitFile, nil)
	_, err := r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.ErrorIs(t, err, migrate.ErrRejected)
	assert.Empty(t, r.pusher.calls)
}

// TestNative writes import blocks into the destinations and removed blocks into the source, then
// checks the plans: imports with no changes and forgets.
func TestNative(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	r.seed(unitNetwork, "network.tfstate")
	r.seed(unitEndpoints, "network-endpoints.tfstate")
	f := parseFile(
		t,
		strings.Replace(splitFile, `from = "live/network"`, "from = \"live/network\"\n  strategy = \"native\"", 1),
		nil,
	)

	rep, err := r.engine(false).Plan(t.Context(), f, f.Blocks)
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultPlanned, rep.Blocks[0].Result)
	assert.Equal(t, "moves_tfcensus_split_network.tf", rep.Blocks[0].Units[0].File)

	imports := func(addrs ...string) []migrate.ResourceChange {
		var out []migrate.ResourceChange
		for _, a := range addrs {
			out = append(out, migrate.ResourceChange{Address: a, Actions: []string{"no-op"}, Importing: true})
		}
		return out
	}
	r.planner.plans[r.units[unitSG].Dir] = imports("aws_security_group.app",
		`aws_vpc_security_group_ingress_rule.app["https"]`, `aws_vpc_security_group_ingress_rule.app["ssh"]`)
	r.planner.plans[r.units[unitEndpoints].Dir] = append(imports("module.endpoints.aws_vpc_endpoint.s3",
		"aws_route53_record.api[0]", "aws_route53_record.api[1]"),
		migrate.ResourceChange{Address: "aws_vpc_endpoint.dynamodb", Actions: []string{"no-op"}})
	var forgets []migrate.ResourceChange
	for _, a := range rep.Blocks[0].Units[2].Moved {
		forgets = append(forgets, migrate.ResourceChange{Address: a, Actions: []string{"forget"}})
	}
	r.planner.plans[r.units[unitNetwork].Dir] = forgets

	rep, err = r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultWritten, rep.Blocks[0].Result)
	assert.Empty(t, rep.Blocks[0].Findings)
	assert.Empty(t, r.pusher.calls, "native changes no state")
	assert.Empty(t, r.s3.writes)
	for _, p := range []string{unitSG, unitEndpoints, unitNetwork} {
		b, readErr := os.ReadFile(filepath.Join(r.units[p].Dir, "moves_tfcensus_split_network.tf"))
		require.NoError(t, readErr)
		testutil.Golden(t, filepath.Join(goldenDir, "native", strings.ReplaceAll(p, "/", "_")+".tf"), b)
	}

	// Without --force the files aren't overwritten; with a create in the plan the block is rejected.
	_, err = r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.Error(t, err)
	r.planner.plans[r.units[unitSG].Dir] = plan(t, "wrong-account.json")
	rep, err = r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), true)
	require.ErrorIs(t, err, migrate.ErrRejected)
	assert.NotEmpty(t, rep.Blocks[0].Findings)
}

func TestNativeRefusesPartialResources(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG)
	r.seed(unitNetwork, "network.tfstate")
	f := parseFile(t, `migration "units" "x" {
  from     = "live/network"
  strategy = "native"
  to       = { "live/network-sg" = ["aws_route53_record.api[0]"] }
}`, nil)
	_, err := r.engine(false).Plan(t.Context(), f, f.Blocks)
	require.ErrorIs(t, err, migrate.ErrNative)
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src, want string
	}{
		{"tfmigrate block", `migration "state" "x" {
  dir = "."
  actions = []
}`, `migration type "state" is not supported`},
		{"both to and actions", `migration "units" "x" {
  from = "a"
  to = { "b" = ["aws_s3_bucket.x"] }
  actions = ["rm aws_s3_bucket.x"]
}`, "set exactly one of to"},
		{"neither", `migration "units" "x" { from = "a" }`, "set exactly one of to"},
		{"unknown attribute", `migration "units" "x" {
  from = "a"
  dir = "b"
  actions = ["rm x.y"]
}`, `An argument named "dir" is not expected here`},
		{"escaping path", `migration "units" "x" {
  from = "../other"
  actions = ["rm x.y"]
}`, "must stay inside the repository"},
		{"absolute path", `migration "units" "x" {
  from = "/live/a"
  actions = ["rm x.y"]
}`, "must be relative"},
		{"source as destination", `migration "units" "x" {
  from = "a"
  to = { "a" = ["x.y"] }
}`, "is the source or listed twice"},
		{"bad address", `migration "units" "x" {
  from = "a"
  to = { "b" = ["aws_s3_bucket"] }
}`, "invalid address"},
		{"import action", `migration "units" "x" {
  from = "a"
  actions = ["import aws_s3_bucket.x bucket"]
}`, "import is not supported"},
		{"native with actions", `migration "units" "x" {
  from = "a"
  strategy = "native"
  actions = ["rm x.y"]
}`, "native strategy moves between units"},
		{"unterminated quote", `migration "units" "x" {
  from = "a"
  actions = ["mv 'x.y z.y"]
}`, "unterminated quote"},
		{"duplicate block", `migration "units" "x" {
  from = "a"
  actions = ["rm x.y"]
}
migration "units" "x" {
  from = "a"
  actions = ["rm x.y"]
}`, "defined twice"},
		{"unset var", `migration "units" "x" {
  from = "live/${var.env}/a"
  actions = ["rm x.y"]
}`, "Unsupported attribute"},
		{"empty", ``, "has no migration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := migrate.Parse([]byte(tt.src), "f.hcl", nil, nil)
			require.ErrorIs(t, err, migrate.ErrFile)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
	f, err := migrate.Parse([]byte(`
migration "units" "a" {
  from = "live/${env.TFCENSUS_ENV}/network/"
  to = {
    "live/${var.env}/z" = ["aws_s3_bucket.x"]
    "live/${var.env}/b" = ["module.m[*]"]
  }
}
`), "f.hcl", map[string]string{"TFCENSUS_ENV": "env1"}, map[string]string{"env": "env1"})
	require.NoError(t, err)
	b := f.Blocks[0]
	assert.Equal(t, "live/env1/network", b.From)
	assert.Equal(t, migrate.StrategySurgery, b.Strategy)
	assert.Equal(t, []string{"live/env1/z", "live/env1/b", "live/env1/network"}, b.Units(), "file order kept")
	assert.Equal(t, "live/env1/b", b.ConfirmPath())

	_, err = migrate.Select(f, []string{"missing"})
	require.ErrorIs(t, err, migrate.ErrFile)
}

type failingPusher struct {
	*fakePusher

	fail string
}

func (p *failingPusher) Push(ctx context.Context, dir, file string) error {
	if dir == p.fail {
		return errors.New("push failed")
	}
	return p.fakePusher.Push(ctx, dir, file)
}
