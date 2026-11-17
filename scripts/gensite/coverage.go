package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// layerDocs says what each inventory layer lists and when it runs, keyed by layer name.
func layerDocs() map[string]string {
	return map[string]string{
		inventory.LayerConfig:         "AWS Config's recorded resources, through `SelectResourceConfig` (or the aggregator's `SelectAggregateResourceConfig`). Runs where a recorder is recording.",
		inventory.LayerRE:             "Resource Explorer's index, through `ListResources` on the default view. Runs where an index has a default view.",
		inventory.LayerSGRules:        "Security group rules, through `ec2:DescribeSecurityGroupRules`. AWS Config reports them only inside the group.",
		inventory.LayerNATGateways:    "The route tables that regional NAT gateways create, through `ec2:DescribeNatGateways`.",
		inventory.LayerIAMAttachments: "Managed policy attachments and inline policies of roles, users and groups, through `iam:GetAccountAuthorizationDetails`.",
		inventory.LayerRoute53Records: "Record sets of every hosted zone, through `route53:ListResourceRecordSets`.",
		inventory.LayerS3Subresources: "Bucket policy, versioning, lifecycle, encryption and public access block, which Terraform manages as resources of their own, through the S3 `GetBucket*` calls.",
		inventory.LayerEKSPodIdentity: "EKS Pod Identity associations of the clusters another layer found, through `eks:ListPodIdentityAssociations`.",
		inventory.LayerCC:             "Cloud Control `ListResources` for the types that neither AWS Config nor Resource Explorer lists, plus the types in `scan.cloudcontrol_types`. Runs everywhere.",
	}
}

// coverageEnv is the inventory environment of a scan with the default config, whose Cloud
// Control layer lists the types only Cloud Control lists.
func coverageEnv(m *typemap.Map) *inventory.Env {
	return &inventory.Env{Map: m, CloudControlTypes: m.CCTypes()}
}

func layersPart(m *typemap.Map) ([]byte, error) {
	docs := layerDocs()
	aliases := map[string]string{}
	for short, name := range inventory.LayerAliases() {
		aliases[name] = short
	}
	var rows [][]string
	for _, l := range inventory.Layers(coverageEnv(m)) {
		desc, ok := docs[l.Name()]
		if !ok {
			return nil, fmt.Errorf("gensite: layerDocs has no description for layer %s", l.Name())
		}
		names := []string{l.Name()}
		if a := aliases[l.Name()]; a != "" {
			names = append(names, a)
		}
		scope := "each region"
		if l.Global() {
			scope = "once per account"
		}
		types := strconv.Itoa(len(l.Types()))
		if strings.HasPrefix(l.Name(), inventory.LayersNative+":") {
			types = codeList(l.Types())
		}
		rows = append(rows, []string{codeList(names), scope, types, desc})
	}
	var b strings.Builder
	b.WriteString(partNote)
	table(&b, []string{"Layer", "Runs", "Types", "Lists"}, rows)
	fmt.Fprintf(&b, "The layers are in precedence order: when two report the same resource, the earlier one's "+
		"attributes win. `--layers` takes layer names, `%s` for every native layer and `%s` (the default) "+
		"for every layer the capability probe allows.\n", inventory.LayersNative, inventory.LayersAuto)
	return []byte(b.String()), nil
}

// coverageRow is one Terraform type of the type coverage table. Short JSON names keep the file
// small.
type coverageRow struct {
	TF      string   `json:"tf"`
	Service string   `json:"svc"`
	Mapped  bool     `json:"mapped"`
	CFN     []string `json:"cfn,omitempty"`
	Config  string   `json:"config,omitempty"`
	RE      []string `json:"re,omitempty"`
	Layers  []string `json:"layers,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	Note    string   `json:"note,omitempty"`
}

type coverageData struct {
	Source   string        `json:"source"`
	Types    int           `json:"types"`
	Mapped   int           `json:"mapped"`
	Provider string        `json:"provider"`
	Layers   []string      `json:"layers"`
	Rows     []coverageRow `json:"rows,omitempty"`
}

// matchKeys returns the keys tfcensus can match a type on, in the order it tries them.
func matchKeys(t *typemap.Type) []string {
	var keys []string
	if len(t.IdentityAttrs) > 0 {
		keys = append(keys, "identity")
	}
	if t.ARN != nil {
		keys = append(keys, "arn")
	}
	if len(t.ID) > 0 {
		keys = append(keys, "id")
	}
	if t.Tuple {
		keys = append(keys, "rule tuple")
	}
	return keys
}

func typeCoverage(m *typemap.Map) ([]byte, error) {
	layers := inventory.Layers(coverageEnv(m))
	listedBy := map[string][]string{}
	var layerNames []string
	for _, l := range layers {
		layerNames = append(layerNames, l.Name())
		for _, c := range l.Types() {
			listedBy[c] = append(listedBy[c], l.Name())
		}
	}
	d := coverageData{Source: "internal/typemap/data", Layers: layerNames}
	for _, s := range m.Sources {
		d.Provider = strings.TrimSpace(d.Provider + " " + s.Repo + " " + s.Version)
	}
	for _, tf := range m.TFTypes() {
		t, _ := m.Type(tf)
		row := coverageRow{TF: tf, Service: t.Service, Mapped: t.Curated}
		d.Types++
		if t.Curated {
			d.Mapped++
			row.CFN, row.Config, row.RE, row.Keys = t.CFN, t.Config, t.RE, matchKeys(t)
			set := map[string]bool{}
			for _, c := range t.CFN {
				for _, l := range listedBy[c] {
					set[l] = true
				}
			}
			row.Layers = slices.SortedFunc(maps.Keys(set), func(a, b string) int {
				return slices.Index(layerNames, a) - slices.Index(layerNames, b)
			})
			switch {
			case t.Unlisted != "":
				row.Note = "not listed: " + t.Unlisted
			case t.Derived != nil:
				row.Note = "derived from " + t.Derived.Parent
			}
		}
		d.Rows = append(d.Rows, row)
	}
	return encodeCoverage(&d)
}

// encodeCoverage writes d as JSON with one row per line, so a diff shows the types that changed.
func encodeCoverage(d *coverageData) ([]byte, error) {
	rows := d.Rows
	d.Rows = nil
	head, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode type coverage: %w", err)
	}
	var b strings.Builder
	b.Write(bytes.TrimSuffix(head, []byte("\n}")))
	b.WriteString(",\n  \"rows\": [\n")
	for i, r := range rows {
		line, merr := json.Marshal(r)
		if merr != nil {
			return nil, fmt.Errorf("encode type coverage: %w", merr)
		}
		b.WriteString("    " + string(line))
		if i < len(rows)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ]\n}\n")
	return []byte(b.String()), nil
}
