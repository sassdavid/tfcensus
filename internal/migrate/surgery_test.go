package migrate_test

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/migrate"
	"github.com/sassdavid/tfcensus/internal/testutil"
)

const (
	unitNetwork   = "live/network"
	unitSG        = "live/network-sg"
	unitEndpoints = "live/network-endpoints"
	lineageSource = "6b1f0a52-0d1e-4c8e-9a0b-6c2f6e1d2a01"
	lineageEndpts = "6b1f0a52-0d1e-4c8e-9a0b-6c2f6e1d2a02"
	defaultAWS    = `provider["registry.terraform.io/hashicorp/aws"]`
)

const splitFile = `
migration "units" "split_network" {
  from = "live/network"
  to = {
    "live/network-sg"        = ["aws_security_group.app", "aws_vpc_security_group_ingress_rule.app[*]"]
    "live/network-endpoints" = ["module.endpoints", "aws_route53_record.api"]
  }
}
`

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// stateSummary is what a golden records of a state: everything but attribute values.
type stateSummary struct {
	Serial    uint64            `json:"serial"`
	Lineage   string            `json:"lineage"`
	Instances []instanceSummary `json:"instances"`
}

type instanceSummary struct {
	Module   string          `json:"module,omitempty"`
	Mode     string          `json:"mode"`
	Type     string          `json:"type"`
	Name     string          `json:"name"`
	Each     string          `json:"each,omitempty"`
	Key      json.RawMessage `json:"index_key,omitempty"`
	Provider string          `json:"provider"`
	Deps     json.RawMessage `json:"dependencies,omitempty"`
}

func summarize(st *migrate.State) stateSummary {
	s := stateSummary{Serial: st.Serial, Lineage: st.Lineage, Instances: []instanceSummary{}}
	if uuidRe.MatchString(s.Lineage) && s.Lineage != lineageSource && s.Lineage != lineageEndpts {
		s.Lineage = "(new)"
	}
	for _, r := range st.Resources {
		for _, inst := range r.Instances {
			s.Instances = append(s.Instances, instanceSummary{
				Module: r.Module, Mode: r.Mode, Type: r.Type, Name: r.Name, Each: r.Each,
				Key: inst["index_key"], Provider: r.Provider, Deps: inst["dependencies"],
			})
		}
	}
	return s
}

func goldenStates(t *testing.T, name string, states map[string]*migrate.State) {
	t.Helper()
	out := map[string]stateSummary{}
	for k, st := range states {
		out[k] = summarize(st)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join(goldenDir, name), append(b, '\n'))
}

func instance(t *testing.T, st *migrate.State, module, typ, name string) map[string]json.RawMessage {
	t.Helper()
	for _, r := range st.Resources {
		if r.Module == module && r.Type == typ && r.Name == name {
			return r.Instances[0]
		}
	}
	t.Fatalf("no %s %s.%s in the state", module, typ, name)
	return nil
}

// TestSpec_move_1: state surgery on fixture states moves resources between units with the
// provider rewritten, serials bumped, each lineage kept, module paths and count and for_each keys
// intact, and data sources left in the source.
func TestSpec_move_1(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	srcBytes := r.seed(unitNetwork, "network.tfstate")
	r.seed(unitEndpoints, "network-endpoints.tfstate")
	f := parseFile(t, splitFile, nil)

	rep, err := r.engine(false).Plan(t.Context(), f, f.Blocks)
	require.NoError(t, err)
	require.Len(t, rep.Blocks, 1)
	br := rep.Blocks[0]
	assert.Equal(t, migrate.ResultVerified, br.Result)
	assert.Empty(t, br.Findings)
	assert.Equal(t, []string{"module.endpoints.data.aws_iam_policy_document.s3"}, br.Units[2].Skipped)
	assert.Equal(t, []string{
		`live/network-endpoints: provider provider["registry.terraform.io/hashicorp/aws"].dns -> ` + defaultAWS,
	}, br.Notes)
	assert.Empty(t, r.s3.writes, "plan writes nothing")

	src, sg, endpoints := r.captured(unitNetwork), r.captured(unitSG), r.captured(unitEndpoints)
	assert.Equal(t, lineageSource, src.Lineage)
	assert.Equal(t, uint64(13), src.Serial)
	assert.JSONEq(t, `{"vpc_id":{"value":"vpc-0a1b2c3d4e5f60001","type":"string"}}`, string(src.Outputs))
	assert.Equal(t, lineageEndpts, endpoints.Lineage)
	assert.Equal(t, uint64(5), endpoints.Serial)
	assert.Regexp(t, uuidRe, sg.Lineage)
	assert.NotEqual(t, lineageSource, sg.Lineage)
	assert.Equal(t, uint64(1), sg.Serial)
	assert.Equal(t, "1.12.2", sg.TerraformVersion)

	// Moved instances keep every field, identity included, byte for byte.
	orig, err := migrate.ParseState(srcBytes)
	require.NoError(t, err)
	want, err := json.Marshal(instance(t, orig, "", "aws_security_group", "app"))
	require.NoError(t, err)
	got, err := json.Marshal(instance(t, sg, "", "aws_security_group", "app"))
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))

	goldenStates(t, "surgery-split.json", map[string]*migrate.State{
		unitNetwork: src, unitSG: sg, unitEndpoints: endpoints,
	})
}

// TestSurgeryActions runs tfmigrate actions inside one state: module, resource and instance moves,
// xmv with $1, a count index turned into separate resources, and rm.
func TestSurgeryActions(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork)
	r.seed(unitNetwork, "network.tfstate")
	f := parseFile(t, `
migration "units" "rename" {
  from = "live/network"
  actions = [
    "mv module.endpoints module.vpce",
    "xmv aws_vpc_security_group_ingress_rule.app[*] aws_vpc_security_group_ingress_rule.web[$1]",
    "mv aws_route53_record.api[0] aws_route53_record.primary",
    "mv 'aws_route53_record.api[1]' 'module.dns[\"b c\"].aws_route53_record.api'",
    "mv aws_vpc.main aws_vpc.this",
    "rm module.vpce.data.aws_iam_policy_document.s3",
  ]
}
`, nil)
	rep, err := r.engine(false).Plan(t.Context(), f, f.Blocks)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"module.endpoints -> module.vpce",
		`aws_vpc_security_group_ingress_rule.app["https"] -> aws_vpc_security_group_ingress_rule.web["https"]`,
		`aws_vpc_security_group_ingress_rule.app["ssh"] -> aws_vpc_security_group_ingress_rule.web["ssh"]`,
		"aws_route53_record.api[0] -> aws_route53_record.primary",
		`aws_route53_record.api[1] -> module.dns["b c"].aws_route53_record.api`,
		"aws_vpc.main -> aws_vpc.this",
		"module.vpce.data.aws_iam_policy_document.s3",
	}, rep.Blocks[0].Units[0].Moved)
	st := r.captured(unitNetwork)
	assert.Equal(t, lineageSource, st.Lineage)
	assert.Equal(t, uint64(13), st.Serial)
	goldenStates(t, "surgery-actions.json", map[string]*migrate.State{unitNetwork: st})
}

func TestSurgeryErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file string
		want       error
	}{
		{
			name: "address only the destination has",
			file: `migration "units" "x" {
  from = "live/network"
  to   = { "live/network-endpoints" = ["aws_vpc_endpoint.dynamodb"] }
}`,
			want: migrate.ErrNoMatch,
		},
		{
			name: "type change",
			file: `migration "units" "x" {
  from    = "live/network"
  actions = ["mv aws_security_group.app aws_vpc.main"]
}`,
			want: nil, // type change, checked by message below
		},
		{
			name: "selector matches nothing",
			file: `migration "units" "x" {
  from = "live/network"
  to   = { "live/network-sg" = ["aws_security_group.missing"] }
}`,
			want: migrate.ErrNoMatch,
		},
		{
			name: "named data source",
			file: `migration "units" "x" {
  from = "live/network"
  to   = { "live/network-sg" = ["module.endpoints.data.aws_iam_policy_document.s3"] }
}`,
			want: migrate.ErrDataSource,
		},
		{
			name: "xmv matches nothing",
			file: `migration "units" "x" {
  from    = "live/network"
  actions = ["xmv aws_lb.* aws_lb.new_$1"]
}`,
			want: migrate.ErrNoMatch,
		},
		{
			name: "rm of a missing address",
			file: `migration "units" "x" {
  from    = "live/network"
  actions = ["rm aws_lb.gone"]
}`,
			want: migrate.ErrNoMatch,
		},
		{
			name: "instance onto a taken key",
			file: `migration "units" "x" {
  from    = "live/network"
  actions = ["mv aws_route53_record.api[0] aws_route53_record.api[1]"]
}`,
			want: migrate.ErrConflict,
		},
		{
			name: "move onto an existing resource",
			file: `migration "units" "x" {
  from = "live/network"
  to   = { "live/network-endpoints" = ["module.endpoints"] }
}
migration "units" "y" {
  from    = "live/network-endpoints"
  actions = ["mv aws_vpc_endpoint.dynamodb module.endpoints.aws_vpc_endpoint.s3"]
}`,
			want: migrate.ErrConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, unitNetwork, unitSG, unitEndpoints)
			r.seed(unitNetwork, "network.tfstate")
			r.seed(unitEndpoints, "network-endpoints.tfstate")
			f := parseFile(t, tt.file, nil)
			_, err := r.engine(false).Plan(t.Context(), f, f.Blocks)
			require.Error(t, err)
			if tt.want != nil {
				assert.ErrorIs(t, err, tt.want)
			} else {
				assert.Contains(t, err.Error(), "type and mode must stay the same")
			}
		})
	}
}

// TestSurgeryOutputsIgnored: a plan whose only differences are outputs and data reads passes.
func TestSurgeryOutputsIgnored(t *testing.T) {
	t.Parallel()
	r := newRig(t, unitNetwork, unitSG, unitEndpoints)
	r.seed(unitNetwork, "network.tfstate")
	r.seed(unitEndpoints, "network-endpoints.tfstate")
	r.planner.plans[r.units[unitNetwork].Dir] = plan(t, "outputs-only.json")
	f := parseFile(t, splitFile, nil)
	rep, err := r.engine(false).Plan(t.Context(), f, f.Blocks)
	require.NoError(t, err)
	assert.Empty(t, rep.Blocks[0].Findings)
}
