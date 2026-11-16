package inventory_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

func txtSet(name, value string) string {
	return `<ResourceRecordSet><Name>` + name + `</Name><Type>TXT</Type><TTL>300</TTL><ResourceRecords>` +
		`<ResourceRecord><Value>&quot;` + value + `&quot;</Value></ResourceRecord></ResourceRecords></ResourceRecordSet>`
}

func aliasSet(name string) string {
	return `<ResourceRecordSet><Name>` + name + `</Name><Type>A</Type><AliasTarget><HostedZoneId>Z2</HostedZoneId>` +
		`<DNSName>web-1.eu-west-1.elb.amazonaws.com.</DNSName><EvaluateTargetHealth>false</EvaluateTargetHealth>` +
		`</AliasTarget></ResourceRecordSet>`
}

// TestSpec_scan_accuracy_3_externalDNS: external-dns ownership with the default naming, a prefix,
// a suffix and a %{record_type} template; the legacy cname- token on an A ALIAS record; an
// encrypted value, which counts only when the record it names exists. Values are never kept.
func TestSpec_scan_accuracy_3_externalDNS(t *testing.T) {
	t.Parallel()

	const heritage = "heritage=external-dns,external-dns/owner=main,external-dns/resource=ingress/web/app"
	const encrypted = "ZXhhbXBsZS1lbmNyeXB0ZWQtb3duZXItcmVjb3JkLXZhbHVl"
	tests := []struct {
		name   string
		cfg    inventory.ExternalDNS
		sets   []string
		marked []string
	}{
		{
			name: "default", sets: []string{
				txtSet("a-app.example.com.", heritage), recordSet("app.example.com.", "A", ""),
				txtSet("cname-api.example.com.", heritage), aliasSet("api.example.com."),
				txtSet("external-dns-a-web.example.com.", encrypted), recordSet("web.example.com.", "A", ""),
				txtSet("a-ghost.example.com.", encrypted),
				txtSet("example.com.", "v=spf1 include:_spf.example.net ~all"),
				recordSet("manual.example.com.", "A", ""),
			},
			marked: []string{
				"a-app.example.com TXT", "api.example.com A", "app.example.com A", "cname-api.example.com TXT",
				"external-dns-a-web.example.com TXT", "web.example.com A",
			},
		},
		{
			name: "prefix", cfg: inventory.ExternalDNS{Prefix: "edns-"}, sets: []string{
				txtSet("edns-a-app.example.com.", heritage), recordSet("app.example.com.", "A", ""),
				txtSet("a-other.example.com.", encrypted), recordSet("other.example.com.", "A", ""),
			},
			marked: []string{"app.example.com A", "edns-a-app.example.com TXT"},
		},
		{
			name: "suffix", cfg: inventory.ExternalDNS{Suffix: "-owner"}, sets: []string{
				txtSet("aaaa-app-owner.example.com.", heritage), recordSet("app.example.com.", "AAAA", ""),
			},
			marked: []string{"aaaa-app-owner.example.com TXT", "app.example.com AAAA"},
		},
		{
			name: "template", cfg: inventory.ExternalDNS{Prefix: "%{record_type}-edns-"}, sets: []string{
				txtSet("cname-edns-docs.example.com.", heritage), recordSet("docs.example.com.", "CNAME", ""),
			},
			marked: []string{"cname-edns-docs.example.com TXT", "docs.example.com CNAME"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rt := transport(t, "inventory-external-dns-"+tt.name, func(r *awstest.Router) {
				r.On("route53", "ListHostedZones", "", ok(zones()))
				r.On("route53", "ListResourceRecordSets", "", ok(records("", tt.sets...)))
			})
			e := env(t)
			e.ExternalDNS = tt.cfg
			res, err := inventory.NewCollector(inventory.Options{Env: e, Layers: inventory.NativeLayers(e)}).
				Collect(t.Context(), inventory.Request{
					Targets: []inventory.Target{{Account: account(rt), Report: oneRegion()}},
					Layers:  []string{inventory.LayerRoute53Records},
				})
			require.NoError(t, err)
			var marked []string
			for _, r := range res.Resources {
				for k, v := range r.Attrs {
					assert.NotContains(t, v, "heritage=", "%s %s keeps a value", r.ID, k)
					assert.NotContains(t, v, encrypted, "%s %s keeps a value", r.ID, k)
				}
				if r.Attrs["external_dns"] == "true" {
					marked = append(marked, r.Name+" "+r.Attrs["type"])
				}
			}
			slices.Sort(marked)
			assert.Equal(t, tt.marked, marked)
		})
	}
}
