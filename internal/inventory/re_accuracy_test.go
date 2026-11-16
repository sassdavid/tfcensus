package inventory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

// TestSpec_scan_accuracy_6: a record only Resource Explorer lists, of a type Config recorded in
// the same account and region, is dropped and counted; Kubernetes objects are skipped and
// counted; every Resource Explorer record has an id.
func TestSpec_scan_accuracy_6(t *testing.T) {
	t.Parallel()

	sg := "arn:aws:ec2:eu-west-1:" + acct + ":security-group/sg-1"
	eip := "arn:aws:ec2:eu-west-1:" + acct + ":elastic-ip/eipalloc-gone"
	vpc := "arn:aws:ec2:us-east-1:" + acct + ":vpc/vpc-9"
	k8s := func(region, kind, name string) string {
		return reResource("arn:aws:eks:"+region+":"+acct+":"+kind+"/main/default/"+name, "eks:"+kind, region)
	}
	rt := transport(t, "inventory-re-accuracy", func(r *awstest.Router) {
		r.On("config", "SelectResourceConfig", "", ok(configPage("",
			configRow("AWS::EC2::SecurityGroup", sg, "sg-1", euWest))))
		r.On("resource-explorer-2", "ListResources", "region:eu-west-1", ok(rePage("",
			reResource(sg, "ec2:security-group", euWest),
			reResource(eip, "ec2:elastic-ip", euWest),
			k8s(euWest, "deployment", "api"),
			k8s(euWest, "replicaset", "api-6d4b"),
			reResource("arn:aws:widgets:eu-west-1:"+acct+":gadget/g-1", "widgets:gadget", euWest))))
		r.On("resource-explorer-2", "ListResources", "region:us-east-1", ok(rePage("",
			reResource(vpc, "ec2:vpc", usEast),
			k8s(usEast, "namespace", "kube-system"))))
	})
	e := env(t)
	res, err := inventory.NewCollector(inventory.Options{
		Env: e, Layers: []inventory.Layer{inventory.NewConfig(e), inventory.NewResourceExplorer(e)},
	}).Collect(t.Context(), inventory.Request{Targets: []inventory.Target{{Account: account(rt), Report: twoRegions()}}})
	require.NoError(t, err)

	byARN := map[string]inventory.CloudResource{}
	for _, r := range res.Resources {
		byARN[r.ARN] = r
		assert.NotEmpty(t, r.ID, "%s has no id", r.ARN)
	}
	assert.Contains(t, byARN, sg, "Config listed it too")
	assert.NotContains(t, byARN, eip, "stale: only Resource Explorer listed it, and Config records EIPs")
	assert.Equal(t, "vpc-9", byARN[vpc].ID, "no recorder in us-east-1, so Resource Explorer's record stays")
	assert.Equal(
		t,
		"g-1",
		byARN["arn:aws:widgets:eu-west-1:"+acct+":gadget/g-1"].ID,
		"an unmapped type gets its last ARN segment",
	)
	assert.Len(t, res.Resources, 3)
	assert.Equal(t, []inventory.Note{
		{Account: acct, Region: euWest, Layer: inventory.LayerRE, Reason: inventory.NoteStale, Count: 1},
		{Account: acct, Region: euWest, Layer: inventory.LayerRE, Reason: inventory.NoteKubernetes, Count: 2},
		{Account: acct, Region: usEast, Layer: inventory.LayerRE, Reason: inventory.NoteKubernetes, Count: 1},
	}, res.Coverage.Notes)
}
