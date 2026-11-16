package inventory_test

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

// TestSpec_scan_accuracy_8: the coverage report lists the AWS Config types scan doesn't map, with
// their counts, from paginated GetDiscoveredResourceCounts calls in each recording region; a
// region whose call is denied is reported, and the collection still succeeds.
func TestSpec_scan_accuracy_8(t *testing.T) {
	t.Parallel()

	counts := func(next string, pairs ...string) awstest.Stub {
		items := make([]string, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			items = append(items, `{"resourceType":"`+pairs[i]+`","count":`+pairs[i+1]+`}`)
		}
		body := `{"resourceCounts":[` + strings.Join(items, ",") + `],"totalDiscoveredResources":0`
		if next != "" {
			body += `,"nextToken":"` + next + `"`
		}
		return ok(body + `}`)
	}
	report := twoRegions()
	report.Regions[0].Config = report.Regions[1].Config // us-east-1 records too, and is denied
	rt := transport(t, "inventory-config-counts", func(r *awstest.Router) {
		r.On("config", "SelectResourceConfig", "", ok(configPage("")), ok(configPage("")))
		r.On("config", "GetDiscoveredResourceCounts", "config.us-east-1.", fail(http.StatusBadRequest,
			`{"__type":"AccessDeniedException","message":"not authorized"}`))
		r.On(
			"config",
			"GetDiscoveredResourceCounts",
			"page-2",
			counts("", "AWS::ECS::Cluster", "2", "AWS::SSM::Document", "3"),
		)
		r.On("config", "GetDiscoveredResourceCounts", "", counts("page-2",
			"AWS::EC2::VPC", "4", "AWS::IAM::Policy", "12", "AWS::SSM::ManagedInstanceInventory", "9"))
	})
	e := env(t)
	res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: []inventory.Layer{inventory.NewConfig(e)}}).
		Collect(t.Context(), inventory.Request{
			Targets: []inventory.Target{{Account: account(rt), Report: report}}, CountUnmapped: true,
		})
	require.NoError(t, err)
	assert.Equal(t, []inventory.UnmappedType{
		{Account: acct, Region: euWest, Type: "AWS::SSM::ManagedInstanceInventory", Count: 9},
		{Account: acct, Region: euWest, Type: "AWS::SSM::Document", Count: 3},
		{Account: acct, Region: euWest, Type: "AWS::ECS::Cluster", Count: 2},
	}, res.Coverage.Unmapped, "VPCs and IAM policies (AWS::IAM::ManagedPolicy) are mapped")
	require.Len(t, res.Coverage.Errors, 1)
	assert.Contains(t, res.Coverage.Errors[0], "us-east-1")
	var b bytes.Buffer
	require.NoError(t, inventory.WriteCoverage(&b, res.Coverage))
	testutil.Golden(t, filepath.Join(goldenDir, "coverage-unmapped.golden"), b.Bytes())
}
