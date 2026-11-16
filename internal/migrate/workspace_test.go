package migrate_test

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/migrate"
)

const (
	blueNetwork   = "live/network:blue"
	blueEndpoints = "live/network-endpoints:blue"
	blueKey       = "env:/blue/live/network/terraform.tfstate"
	wsSplitFile   = `
migration "units" "ws_split" {
  from = "live/network:${var.ws}"
  to   = { "live/network-endpoints:${var.ws}" = ["module.endpoints", "aws_route53_record.api"] }
}
`
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, "states", name))
	require.NoError(t, err)
	return b
}

// TestSpec_move_5 moves resources between the blue workspaces of two units: states, locks, digests
// and histories live under env:/blue/, terraform gets the workspace, the default workspace is left
// alone, and rollback restores the blue states byte for byte.
func TestSpec_move_5(t *testing.T) {
	t.Parallel()
	r := newRig(t, blueNetwork, blueEndpoints)
	srcOrig := r.seed(blueNetwork, "network.tfstate")
	endOrig := r.seed(blueEndpoints, "network-endpoints.tfstate")
	defaultKey := "live/network/terraform.tfstate"
	r.s3.put(defaultKey, srcOrig)
	r.units[blueNetwork].Table, r.units[blueNetwork].Lock = testTable, discovery.LockBoth
	require.Equal(t, blueKey, r.units[blueNetwork].Key)
	f := parseFile(t, wsSplitFile, map[string]string{"ws": "blue"})

	rep, err := r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	br := rep.Blocks[0]
	assert.Equal(t, migrate.ResultApplied, br.Result)
	assert.Equal(t, blueNetwork, br.From)
	assert.Equal(t, []string{blueEndpoints, blueNetwork}, r.pusher.calls, "destination, then source")
	for _, u := range br.Units {
		assert.Equal(t, "blue", u.Workspace, u.Path)
	}
	assert.Equal(t, "live/network-endpoints", br.Units[0].Path)
	assert.Equal(t, blueEndpoints, br.Units[0].Label())
	r.captured(blueNetwork)
	r.captured(blueEndpoints)

	h := r.history(blueNetwork)
	require.NotNil(t, h)
	assert.Equal(t, "live/network", h.Unit)
	assert.Equal(t, "blue", h.Workspace)
	_, ok := r.s3.body(t, blueKey+migrate.HistorySuffix)
	assert.True(t, ok, "the history sits next to the workspace's state")
	_, ok = r.s3.body(t, defaultKey+migrate.HistorySuffix)
	assert.False(t, ok, "the default workspace has no history")
	got, _ := r.s3.body(t, defaultKey)
	assert.Equal(t, srcOrig, got, "the default workspace is untouched")

	_, err = r.engine(true).Rollback(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	got, _ = r.s3.body(t, blueKey)
	assert.Equal(t, srcOrig, got, "source restored byte for byte")
	got, _ = r.s3.body(t, r.units[blueEndpoints].Key)
	assert.Equal(t, endOrig, got, "destination restored byte for byte")
	assert.Contains(t, r.s3.writes, "PutObject "+testBucket+"/"+blueKey+".tflock",
		"rollback takes the workspace's lock")
	require.NotEmpty(t, r.s3.copies)
	for _, c := range r.s3.copies {
		assert.Contains(t, c, testBucket+"/env%3A/blue/", "the copy source is URL-encoded")
	}
	sum := md5.Sum(srcOrig)
	digestID := testBucket + "/" + blueKey + "-md5"
	assert.Equal(t, map[string]map[string]string{
		digestID: {"LockID": digestID, "Digest": hex.EncodeToString(sum[:])},
	}, r.table.items)
	for _, w := range r.s3.writes {
		assert.NotContains(t, w, " "+testBucket+"/live/", "no write outside env:/blue/")
	}
}

// TestWorkspaceMustExist: a workspace without a state stops the run before planning, because
// terraform can't push to a workspace that doesn't exist.
func TestWorkspaceMustExist(t *testing.T) {
	t.Parallel()
	r := newRig(t, blueNetwork, blueEndpoints)
	r.seed(blueNetwork, "network.tfstate")
	f := parseFile(t, wsSplitFile, map[string]string{"ws": "blue"})

	_, err := r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.ErrorIs(t, err, migrate.ErrNoWorkspace)
	assert.Contains(t, err.Error(), "terraform workspace new blue")
	assert.Contains(t, err.Error(), "s3://"+testBucket+"/env:/blue/live/network-endpoints/terraform.tfstate")
	assert.Empty(t, r.planner.states, "nothing is planned")
	assert.Empty(t, r.s3.writes)
}

// TestWorkspaceToWorkspace moves resources between two workspaces of one unit directory.
func TestWorkspaceToWorkspace(t *testing.T) {
	t.Parallel()
	r := newRig(t, blueNetwork, "live/network:green")
	r.seed(blueNetwork, "network.tfstate")
	r.s3.put("env:/green/live/network/terraform.tfstate", fixture(t, "network-endpoints.tfstate"))
	f := parseFile(t, `
migration "units" "blue_to_green" {
  from = "live/network:blue"
  to   = { "live/network:green" = ["aws_route53_record.api"] }
}
`, nil)

	rep, err := r.engine(true).Apply(t.Context(), f, f.Blocks, yes(), false)
	require.NoError(t, err)
	assert.Equal(t, migrate.ResultApplied, rep.Blocks[0].Result)
	assert.Equal(t, []string{"live/network:green", blueNetwork}, r.pusher.calls)
	assert.Equal(t, r.units[blueNetwork].Dir, r.units["live/network:green"].Dir)
	assert.NotEqual(t, r.captured(blueNetwork).Lineage, r.captured("live/network:green").Lineage,
		"each workspace keeps its own lineage")
}
