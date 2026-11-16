package inventory_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

const (
	acctB     = "222222222222"
	auditAcct = "333333333333"
	euCentral = "eu-central-1"
)

// accountID returns [account] with another ID.
func accountID(rt http.RoundTripper, id string) *accounts.Account {
	a := account(rt)
	a.ID = id
	return a
}

// recording is a region whose recorder records, with or without global types.
func recording(region string, global bool) probe.Region {
	return probe.Region{
		Region: region,
		Config: probe.ConfigCheck{Status: probe.StatusAvailable, Recorders: []probe.Recorder{
			{Name: "default", Recording: true, IncludeGlobal: global, Frequency: "CONTINUOUS"},
		}},
	}
}

func notRecording(region string) probe.Region {
	return probe.Region{
		Region: region,
		Config: probe.ConfigCheck{Status: probe.StatusUnavailable, Recorders: []probe.Recorder{}},
	}
}

// orgAggregator serves every account in regions through one organization source each.
func orgAggregator(rt http.RoundTripper, regions ...string) *inventory.Aggregator {
	rep := &probe.AggregatorReport{
		Name:    "acme-org",
		Account: auditAcct,
		Region:  euCentral,
		Status:  probe.StatusAvailable,
	}
	for _, r := range regions {
		rep.Sources = append(rep.Sources,
			probe.AggSource{Source: "o-acme", Type: probe.SourceOrganization, Region: r, Status: probe.SyncSucceeded})
	}
	return &inventory.Aggregator{Account: accountID(rt, auditAcct), Report: rep}
}

// aggRow is one SelectAggregateResourceConfig result row of account.
func aggRow(account, cfg, arn, id, region string) string {
	row := fmt.Sprintf(
		`{"resourceId":%q,"resourceName":%q,"resourceType":%q,"arn":%q,"awsRegion":%q,"accountId":%q,"tags":[],"relationships":[]}`,
		id,
		id,
		cfg,
		arn,
		region,
		account,
	)
	b, _ := json.Marshal(row)
	return string(b)
}

func sgARN(account, region, id string) string {
	return "arn:aws:ec2:" + region + ":" + account + ":security-group/" + id
}

// aggregatorStubs serves two pages of aggregate rows from eu-central-1 and the per-account query
// for the pair the aggregator returned nothing for (account A in us-east-1).
func aggregatorStubs(r *awstest.Router) {
	r.On("config", "SelectAggregateResourceConfig", `"NextToken":"page-2"`, ok(configPage("",
		aggRow(acctB, "AWS::EC2::SecurityGroup", sgARN(acctB, euWest, "sg-b"), "sg-b", euWest),
		aggRow(acctB, "AWS::IAM::Role", "arn:aws:iam::"+acctB+":role/app", "AROAB", "global"),
		aggRow(acctB, "AWS::EC2::SecurityGroup", sgARN(acctB, usEast, "sg-b-east"), "sg-b-east", usEast))))
	r.On("config", "SelectAggregateResourceConfig", "", ok(configPage("page-2",
		aggRow(acct, "AWS::EC2::SecurityGroup", sgARN(acct, euWest, "sg-a"), "sg-a", euWest),
		aggRow(acct, "AWS::IAM::Role", "arn:aws:iam::"+acct+":role/app", "AROAA", "global"))))
	r.On("config", "SelectResourceConfig", "", ok(configPage("",
		aggRow(acct, "AWS::EC2::VPC", "arn:aws:ec2:us-east-1:"+acct+":vpc/vpc-a", "vpc-a", usEast))))
}

// TestSpec_inventory_6: the Config aggregator fast path. One paginated aggregate query from the
// aggregator account serves the config layer of two accounts. Global rows go to the region that
// records global types, rows of a pair the probe doesn't allow are dropped, and the pair the
// aggregator returned nothing for is queried in its own account.
func TestSpec_inventory_6(t *testing.T) {
	t.Parallel()

	rt := transport(t, "inventory-aggregator", aggregatorStubs)
	e := env(t)
	res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: []inventory.Layer{inventory.NewConfig(e)}}).
		Collect(t.Context(), inventory.Request{
			Targets: []inventory.Target{
				{Account: accountID(rt, acct), Report: &probe.Report{Account: acct, Regions: []probe.Region{
					recording(euWest, true), recording(usEast, false),
				}}},
				{Account: accountID(rt, acctB), Report: &probe.Report{Account: acctB, Regions: []probe.Region{
					recording(euWest, true), notRecording(usEast),
				}}},
			},
			Aggregator: orgAggregator(rt, euWest, usEast),
		})
	require.NoError(t, err)

	var lines []string
	for i := range res.Resources {
		lines = append(lines, strings.TrimSpace(res.Resources[i].Account+" "+describe(&res.Resources[i])+" "+
			res.Resources[i].Region))
	}
	out, err := json.MarshalIndent(map[string]any{
		"resources": lines, "runs": res.Runs, "aggregator": res.Aggregator,
	}, "", "  ")
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join(goldenDir, "collect-aggregator.golden.json"), append(out, '\n'))

	assert.Equal(t, &inventory.AggregatorRun{
		Name:     "acme-org",
		Account:  auditAcct,
		Region:   euCentral,
		Status:   inventory.RunOK,
		Count:    5,
		Served:   2,
		Fallback: 1,
	}, res.Aggregator)
	for _, run := range res.Runs {
		switch {
		case run.Account == acctB && run.Region == usEast:
			assert.Equal(t, inventory.RunUnsupported, run.Status)
		case run.Account == acct && run.Region == usEast:
			assert.Equal(t, inventory.RunOK, run.Status)
			assert.Empty(t, run.Via, "listed in its own account")
		default:
			assert.Equal(t, inventory.RunOK, run.Status, "%s %s", run.Account, run.Region)
			assert.Equal(t, inventory.ViaAggregator, run.Via, "%s %s", run.Account, run.Region)
			assert.Equal(t, 2, run.Count, "the security group and the global role")
		}
	}
}

// TestAggregatorDenied: when the aggregate query is denied, every pair it would have served is
// queried in its own account, and the result says so.
func TestAggregatorDenied(t *testing.T) {
	t.Parallel()

	rt := awstest.NewRouter(t).
		On("config", "SelectAggregateResourceConfig", "", fail(http.StatusBadRequest,
			awstest.JSONError("AccessDeniedException", "not authorized"))).
		On("config", "SelectResourceConfig", "", ok(configPage("",
			aggRow(acct, "AWS::EC2::SecurityGroup", sgARN(acct, euWest, "sg-a"), "sg-a", euWest))))
	e := env(t)
	res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: []inventory.Layer{inventory.NewConfig(e)}}).
		Collect(t.Context(), inventory.Request{
			Targets: []inventory.Target{{Account: accountID(rt, acct), Report: &probe.Report{
				Account: acct, Regions: []probe.Region{recording(euWest, true)},
			}}},
			Aggregator: orgAggregator(rt, euWest),
		})
	require.NoError(t, err)
	require.NotNil(t, res.Aggregator)
	assert.Equal(t, inventory.RunDenied, res.Aggregator.Status)
	assert.Equal(t, 1, res.Aggregator.Fallback)
	assert.Zero(t, res.Aggregator.Served)
	require.Len(t, res.Runs, 1)
	assert.Equal(t, inventory.RunOK, res.Runs[0].Status)
	assert.Empty(t, res.Runs[0].Via)
	require.Len(t, res.Resources, 1)
	assert.Equal(t, "sg-a", res.Resources[0].ID)
}

// TestAggregatorChunks: an account list too long for one query (AWS caps the expression at 4096
// characters) is split over several queries, with no account in two of them.
func TestAggregatorChunks(t *testing.T) {
	t.Parallel()

	const n = 250
	ids := make([]string, n)
	var targets []inventory.Target
	r := awstest.NewRouter(t)
	var firstRows, lastRows []string
	for i := range ids {
		ids[i] = fmt.Sprintf("1000000%05d", i)
		targets = append(targets, inventory.Target{Account: accountID(r, ids[i]), Report: &probe.Report{
			Account: ids[i], Regions: []probe.Region{recording(euWest, false)},
		}})
	}
	// One route answers the query that holds the last account and one the query that holds the
	// first. A single query would leave a route unused, and a third query would find no route.
	for i := range ids {
		row := aggRow(ids[i], "AWS::EC2::SecurityGroup", sgARN(ids[i], euWest, "sg-"+ids[i]), "sg-"+ids[i], euWest)
		if i < n/2 {
			firstRows = append(firstRows, row)
		} else {
			lastRows = append(lastRows, row)
		}
	}
	r.On("config", "SelectAggregateResourceConfig", "'"+ids[n-1]+"'", ok(configPage("", lastRows...)))
	r.On("config", "SelectAggregateResourceConfig", "'"+ids[0]+"'", ok(configPage("", firstRows...)))
	e := env(t)
	res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: []inventory.Layer{inventory.NewConfig(e)}}).
		Collect(t.Context(), inventory.Request{
			Targets: targets, Aggregator: orgAggregator(r, euWest), Types: []string{"AWS::EC2::SecurityGroup"},
		})
	require.NoError(t, err)
	require.NotNil(t, res.Aggregator)
	assert.Equal(t, inventory.RunOK, res.Aggregator.Status, strings.Join(res.Aggregator.Errors, "; "))
	assert.Equal(t, n, res.Aggregator.Count)
}
