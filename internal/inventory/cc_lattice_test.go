package inventory_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// TestSpec_scan_accuracy_7: Cloud Control lists the cc types, reads a resource configuration's
// type (with GetResource when the list leaves it out), and links a CHILD configuration to the
// ARN configuration it belongs to, so the child of a managed configuration is derived.
func TestSpec_scan_accuracy_7(t *testing.T) {
	t.Parallel()

	lattice := func(kind, id string) string {
		return "arn:aws:vpc-lattice:" + euWest + ":" + acct + ":" + kind + "/" + id
	}
	gw, parent, child := lattice("resourcegateway", "rgw-0a1"), lattice("resourceconfiguration", "rcfg-0a2"),
		lattice("resourceconfiguration", "rcfg-0a3")
	desc := func(arn, props string) string {
		return fmt.Sprintf(`{"Identifier":%q,"Properties":%q}`, arn, props)
	}
	page := func(items ...string) awstest.Stub {
		return ok(`{"ResourceDescriptions":[` + strings.Join(items, ",") + `]}`)
	}
	e := env(t)
	e.CloudControlTypes = e.Map.CCTypes()
	rt := transport(t, "inventory-cc-lattice", func(r *awstest.Router) {
		r.On("cloudcontrolapi", "ListResources", `ResourceGateway"`, page(desc(gw, `{"Arn":"`+gw+`"}`)))
		r.On("cloudcontrolapi", "ListResources", `ResourceConfiguration"`, page(
			desc(parent, `{"Arn":"`+parent+`","ResourceConfigurationType":"ARN"}`),
			desc(child, `{"Arn":"`+child+`"}`)))
		r.On("cloudcontrolapi", "GetResource", "rcfg-0a3", ok(`{"TypeName":"AWS::VpcLattice::ResourceConfiguration",`+
			`"ResourceDescription":`+desc(child, `{"Arn":"`+child+`","ResourceConfigurationType":"CHILD",`+
			`"ResourceConfigurationGroupId":"rcfg-0a2","AmazonManaged":true,"Name":"FIXTURE-SECRET"}`)+`}`))
		r.On("cloudcontrolapi", "ListResources", "ServiceNetworkResourceAssociation", page())
		r.On("cloudcontrolapi", "ListResources", "ServiceNetworkVpcAssociation", page())
	})
	res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: []inventory.Layer{inventory.NewCloudControl(e)}}).
		Collect(t.Context(), inventory.Request{Targets: []inventory.Target{{Account: account(rt), Report: oneRegion()}}})
	require.NoError(t, err)
	require.Len(t, res.Resources, 3)
	byARN := map[string]inventory.CloudResource{}
	for _, r := range res.Resources {
		byARN[r.ARN] = r
		assert.NotContains(t, fmt.Sprint(r), "FIXTURE-SECRET", "only the type, group and flag are read")
	}
	assert.Equal(t, "ARN", byARN[parent].Attrs["resource_configuration_type"])
	c := byARN[child]
	assert.Equal(t, map[string]string{"resource_configuration_type": "CHILD", "amazon_managed": "true"}, c.Attrs)
	require.Len(t, c.Parents, 1)
	assert.Equal(t, parent, c.Parents[0].ARN)

	ix := tfstate.BuildIndex([]tfstate.UnitState{{
		Repo: "live", Path: "lattice", Workspace: tfstate.DefaultWorkspace, Status: tfstate.StatusOK,
		Target: discovery.Target{Account: acct, Region: euWest},
		State: &tfstate.State{Version: 4, Resources: []tfstate.Resource{{
			Mode: tfstate.ModeManaged, Type: "aws_vpclattice_resource_configuration", Name: "db",
			Provider: tfstate.Provider{Source: "hashicorp/aws"}, Instances: []tfstate.Instance{{ID: "rcfg-0a2"}},
		}}},
	}})
	got := map[string]match.Result{}
	for _, r := range match.New(e.Map, ix).Match(res.Resources) {
		got[r.Resource.ARN] = r
	}
	assert.Equal(t, match.CategoryManaged, got[parent].Category, got[parent].Reason)
	assert.Equal(t, match.CategoryDerived, got[child].Category, got[child].Reason)
	assert.Equal(t, match.RuleParent, got[child].Rule)
	assert.Equal(t, match.CategoryUnmatched, got[gw].Category)
}
