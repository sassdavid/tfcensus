package tfstate_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const statesDir = "../../testdata/states"

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(statesDir, name))
	require.NoError(t, err)
	return b
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file      string
		version   string
		serial    uint64
		managed   int
		data      int
		encrypted bool
		check     func(t *testing.T, s *tfstate.State)
	}{
		{
			file: "tf15-network.tfstate", version: "1.5.7", serial: 42, managed: 6, data: 1,
			check: func(t *testing.T, s *tfstate.State) {
				t.Helper()
				subnets := s.Resources[2]
				require.Len(t, subnets.Instances, 2)
				assert.Equal(t, "aws_subnet.private[1]", subnets.Address(&subnets.Instances[1]))
				assert.Equal(t, "subnet-0aa1bb2cc3dd4ee51", subnets.Instances[1].ID)
				assert.Nil(t, subnets.Instances[1].Identity, "Terraform 1.5 writes no identity")
				db := s.Resources[4]
				assert.Equal(t, "a1b2c3d4", db.Instances[1].Deposed)
				logs := s.Resources[5]
				assert.Equal(t, "module.flow_logs.aws_cloudwatch_log_group.this", logs.Address(&logs.Instances[0]))
				assert.Equal(t, tfstate.Provider{Source: "hashicorp/aws", Host: "registry.terraform.io"}, logs.Provider)
			},
		},
		{
			file: "tf112-app.tfstate", version: "1.12.2", serial: 7, managed: 7,
			check: func(t *testing.T, s *tfstate.State) {
				t.Helper()
				web := s.Resources[0].Instances[0]
				assert.Equal(t, map[string]string{
					"account_id": "111111111111", "id": "i-0fedcba9876543210", "region": "eu-west-1",
				}, web.Identity)
				assert.Equal(t, "eu-west-1", web.Region)
				ssm := s.Resources[4]
				assert.Equal(t, `aws_ssm_parameter.config["db_password"]`, ssm.Address(&ssm.Instances[0]))
				assert.Equal(t, "use1", s.Resources[5].Provider.Alias)
				assert.Equal(t, "hashicorp/random", s.Resources[6].Provider.Source)
			},
		},
		{
			file: "tofu110-edge.tfstate", version: "1.10.6", serial: 19, managed: 2,
			check: func(t *testing.T, s *tfstate.State) {
				t.Helper()
				p := s.Resources[0].Provider
				assert.Equal(t, "hashicorp/aws", p.Source, "the OpenTofu registry address normalizes")
				assert.True(t, p.OpenTofu())
			},
		},
		{file: "tf111-empty.tfstate", version: "1.11.4", serial: 2},
		{file: "tofu-encrypted.tfstate", serial: 12, encrypted: true},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			s, err := tfstate.Parse(bytes.NewReader(readFixture(t, tt.file)))
			require.NoError(t, err)
			assert.Equal(t, tt.version, s.TerraformVersion)
			assert.Equal(t, tt.serial, s.Serial)
			assert.Equal(t, tt.encrypted, s.Encrypted)
			assert.NotEmpty(t, s.Lineage)
			assert.Len(t, s.Managed(), tt.managed)
			assert.Len(t, s.Resources, tt.managed+tt.data)
			if tt.check != nil {
				tt.check(t, s)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	s, err := tfstate.Parse(bytes.NewReader(readFixture(t, "tf011-v3.tfstate")))
	require.ErrorIs(t, err, tfstate.ErrUnsupportedVersion)
	require.ErrorContains(t, err, "unsupported state format version 3")
	assert.Equal(t, uint64(88), s.Serial)
	assert.NotContains(t, err.Error(), "FIXTURE-SECRET")

	for _, in := range []string{``, `[]`, `{"version": "4"}`, `{"resources": [{"instances": {}}]}`, `{"version": 4`} {
		_, err = tfstate.Parse(strings.NewReader(in))
		assert.Error(t, err, in)
	}
}

func TestParseFlatAttributes(t *testing.T) {
	t.Parallel()

	s, err := tfstate.Parse(strings.NewReader(`{"version":4,"resources":[{"mode":"managed","type":"aws_s3_bucket",` +
		`"name":"logs","provider":"provider.aws","instances":[{"attributes_flat":{"id":"acme-logs","acl":"private"}}]}]}`))
	require.NoError(t, err)
	r := s.Resources[0]
	assert.Equal(t, "acme-logs", r.Instances[0].ID)
	assert.Equal(t, "hashicorp/aws", r.Provider.Source, "legacy provider.aws means the hashicorp namespace")
}

func TestParseProvider(t *testing.T) {
	t.Parallel()

	tests := map[string]tfstate.Provider{
		`provider["registry.terraform.io/hashicorp/aws"]`: {
			Source: "hashicorp/aws",
			Host:   "registry.terraform.io",
		},
		`provider["registry.opentofu.org/hashicorp/aws"].east`: {
			Source: "hashicorp/aws",
			Alias:  "east",
			Host:   "registry.opentofu.org",
		},
		`provider["example.com/acme/internal"]`: {
			Source: "example.com/acme/internal",
			Host:   "example.com",
		},
		`provider.aws.west`: {
			Source: "hashicorp/aws",
			Alias:  "west",
			Host:   "registry.terraform.io",
		},
	}
	for raw, want := range tests {
		assert.Equal(t, want, tfstate.ParseProvider(raw), raw)
	}
}

func TestReadLockFile(t *testing.T) {
	t.Parallel()

	got, err := tfstate.ReadLockFile("../../testdata/repos/tf-fleet/app")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"hashicorp/aws": "6.20.0", "hashicorp/random": "3.7.2"}, got)

	got, err = tfstate.ReadLockFile(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, tfstate.LockFileName), []byte("provider {"), 0o600))
	_, err = tfstate.ReadLockFile(dir)
	assert.ErrorContains(t, err, "parse lock file")
}

func TestWorkspaceKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "app/terraform.tfstate", tfstate.WorkspaceKey("", "app/terraform.tfstate", "default"))
	assert.Equal(t, "env:/staging/app/terraform.tfstate", tfstate.WorkspaceKey("", "app/terraform.tfstate", "staging"))
	assert.Equal(t, "workspaces/qa/app.tfstate", tfstate.WorkspaceKey("workspaces", "app.tfstate", "qa"))
}

func FuzzParse(f *testing.F) {
	for _, name := range []string{"tf15-network.tfstate", "tf112-app.tfstate", "tofu-encrypted.tfstate", "tf011-v3.tfstate"} {
		f.Add(readFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := tfstate.Parse(bytes.NewReader(data))
		if err == nil && s == nil {
			t.Fatal("nil state without an error")
		}
	})
}
