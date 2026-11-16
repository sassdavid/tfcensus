package tfstate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// tofuDir holds the output of a real tofu, recorded by scripts/record-tofu.sh.
const tofuDir = "../../testdata/tofu/secure"

// tofuSecrets are attribute values in the recorded state that show -json prints and no output
// of tfcensus may contain.
func tofuSecrets() []string {
	return []string{"FIXTURE-SECRET", "10.77.0.0/16", "db.t4g.small", "acme_admin", "AROAFIXTURE"}
}

func readTofu(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(tofuDir, name))
	require.NoError(t, err)
	return b
}

// TestParseShowRecorded reads the show -json output of the recorded encrypted state: identifiers
// and sensitive attribute names are kept, every other value and the outputs are dropped.
func TestParseShowRecorded(t *testing.T) {
	t.Parallel()

	s, err := tfstate.ParseShow(strings.NewReader(string(readTofu(t, "show.json"))), tfstate.ParseOptions{})
	require.NoError(t, err)
	assert.Equal(t, "1.13.9", s.TerraformVersion)
	assert.Equal(t, tfstate.SupportedVersion, s.Version)
	assert.False(t, s.Encrypted)

	want := []tfstate.Resource{
		{
			Mode: tfstate.ModeData, Type: "aws_caller_identity", Name: "current",
			Instances: []tfstate.Instance{{
				ID: "111111111111", ARN: "arn:aws:sts::111111111111:assumed-role/acme-ci/session",
			}},
		},
		{
			Mode: tfstate.ModeManaged, Type: "aws_db_instance", Name: "db",
			Instances: []tfstate.Instance{{
				ID: "db-FIXTURESECUREDB0001", ARN: "arn:aws:rds:eu-west-1:111111111111:db:secure-db",
				Region: "eu-west-1", Sensitive: []string{"password"},
			}},
		},
		{
			Mode: tfstate.ModeManaged, Type: "aws_vpc", Name: "main",
			Instances: []tfstate.Instance{{
				ID: "vpc-0f1e2d3c4b5a69788", ARN: "arn:aws:ec2:eu-west-1:111111111111:vpc/vpc-0f1e2d3c4b5a69788",
				Region: "eu-west-1",
			}},
		},
		{
			Mode: tfstate.ModeManaged, Type: "aws_sqs_queue", Name: "this", Module: "module.queue",
			Instances: []tfstate.Instance{{
				IndexKey: `"orders"`, ID: "https://sqs.eu-west-1.amazonaws.com/111111111111/secure-orders",
				ARN: "arn:aws:sqs:eu-west-1:111111111111:secure-orders", Region: "eu-west-1",
			}},
		},
	}
	for i := range want {
		want[i].Provider = tfstate.Provider{Source: "hashicorp/aws", Host: "registry.opentofu.org"}
	}
	assert.Equal(t, want, s.Resources)
	dump := fmt.Sprintf("%#v", s)
	for _, v := range tofuSecrets() {
		assert.NotContains(t, dump, v)
	}
}

func TestParseShow(t *testing.T) {
	t.Parallel()

	const prov = `"provider_name":"registry.terraform.io/hashicorp/aws"`
	tests := []struct {
		name  string
		doc   string
		check func(t *testing.T, s *tfstate.State)
	}{
		{
			name: "empty state has no values",
			doc:  `{"format_version":"1.0"}`,
			check: func(t *testing.T, s *tfstate.State) {
				t.Helper()
				assert.Empty(t, s.Resources)
			},
		},
		{
			name: "instances of one resource become one resource",
			doc: `{"values":{"root_module":{"resources":[
			  {"address":"aws_subnet.a[0]","mode":"managed","type":"aws_subnet","name":"a","index":0,` + prov + `,"values":{"id":"subnet-1"}},
			  {"address":"aws_subnet.a[1]","mode":"managed","type":"aws_subnet","name":"a","index":1,` + prov + `,"values":{"id":"subnet-2"}},
			  {"address":"aws_subnet.a[1]","mode":"managed","type":"aws_subnet","name":"a","index":1,` + prov + `,"deposed_key":"00000001","values":{"id":"subnet-0"}}]}}}`,
			check: func(t *testing.T, s *tfstate.State) {
				t.Helper()
				require.Len(t, s.Resources, 1)
				assert.Equal(t, []tfstate.Instance{
					{IndexKey: "0", ID: "subnet-1"},
					{IndexKey: "1", ID: "subnet-2"},
					{IndexKey: "1", ID: "subnet-0", Deposed: "00000001"},
				}, s.Resources[0].Instances)
				assert.Equal(t, "hashicorp/aws", s.Resources[0].Provider.Source)
			},
		},
		{
			name: "nested modules come from the address",
			doc: `{"values":{"root_module":{"child_modules":[{"resources":[],"address":"module.a[\"x\"]","child_modules":[
			  {"resources":[{"address":"module.a[\"x\"].module.b[0].data.aws_region.this","mode":"data","type":"aws_region","name":"this",` + prov + `,"values":{"id":"eu-west-1"}}],
			   "address":"module.a[\"x\"].module.b[0]"}]}]}}}`,
			check: func(t *testing.T, s *tfstate.State) {
				t.Helper()
				require.Len(t, s.Resources, 1)
				assert.Equal(t, `module.a["x"].module.b[0]`, s.Resources[0].Module)
				assert.Equal(t, tfstate.ModeData, s.Resources[0].Mode)
			},
		},
		{
			name: "sensitive names need a set value and no value is kept",
			doc: `{"values":{"outputs":{"o":{"sensitive":true,"value":"VALUE-ONE"}},"root_module":{"resources":[
			  {"address":"aws_db_instance.db","mode":"managed","type":"aws_db_instance","name":"db",` + prov + `,
			   "values":{"id":"db-1","password":"VALUE-TWO","password_wo":null,"connection":{"token":"VALUE-THREE"},"tags":{"a":"VALUE-FOUR"}},
			   "sensitive_values":{"password":true,"password_wo":true,"connection":{"token":true},"tags":{},"list":[false,[true]]},
			   "identity":{"id":"db-1","account_id":"111111111111"}}]}}}`,
			check: func(t *testing.T, s *tfstate.State) {
				t.Helper()
				inst := s.Resources[0].Instances[0]
				assert.Equal(t, []string{"connection", "password"}, inst.Sensitive)
				assert.Equal(t, map[string]string{"id": "db-1", "account_id": "111111111111"}, inst.Identity)
				assert.NotContains(t, fmt.Sprintf("%#v", s), "VALUE")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := tfstate.ParseShow(strings.NewReader(tt.doc), tfstate.ParseOptions{})
			require.NoError(t, err)
			tt.check(t, s)
		})
	}
}

func TestParseShowErrors(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{``, `[]`, `{"values":{"root_module":{"resources":[{"index":{}}]}}}`, `{"values":`} {
		_, err := tfstate.ParseShow(strings.NewReader(doc), tfstate.ParseOptions{})
		require.Error(t, err, doc)
	}
}

// TestParseRecordedEncrypted checks detection on a state that a real tofu encrypted: only the
// serial and lineage are readable.
func TestParseRecordedEncrypted(t *testing.T) {
	t.Parallel()

	s, err := tfstate.Parse(strings.NewReader(string(readTofu(t, "encrypted.tfstate"))))
	require.NoError(t, err)
	assert.True(t, s.Encrypted)
	assert.Equal(t, uint64(13), s.Serial)
	assert.Equal(t, "e6f70819-2a3b-4c4d-9e5f-607182930415", s.Lineage)
	assert.Empty(t, s.Resources)
}
