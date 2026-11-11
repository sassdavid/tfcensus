package audit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// TestFleetVersions runs the version checks on states alone: no unit, so no AWS call.
func TestFleetVersions(t *testing.T) {
	t.Parallel()

	policy := config.AuditPolicy{
		MinTerraformVersion: "1.10",
		MinProviderVersions: map[string]string{"hashicorp/aws": "5.0"},
		MaxStateMB:          1,
	}
	tests := []struct {
		version, aws string
		size         int64
		want         []string
	}{
		{"1.10.0", "5.0.0", 100, nil},
		{"1.10.0-beta1", "5.100.0", 100, nil},
		{"1.9.8", "5.0.0", 100, []string{"FL-TFVER"}},
		{"1.11.0", "4.67.0", 100, []string{"FL-PROV"}},
		{"v1.12.0", "6.0.0", 2 << 20, []string{"FL-SIZE"}},
	}
	for _, tt := range tests {
		st := tfstate.UnitState{
			Repo: "live", Path: "app", Workspace: tfstate.DefaultWorkspace, Status: tfstate.StatusOK,
			TerraformVersion: tt.version, Instances: 1, Size: tt.size,
			Providers: []tfstate.ProviderVersion{{Source: "hashicorp/aws", Version: tt.aws}},
		}
		rep, err := audit.Run(t.Context(), audit.Options{Policy: policy, Now: time.Now()},
			audit.Input{States: []tfstate.UnitState{st}})
		require.NoError(t, err)
		var got []string
		for _, f := range rep.Findings {
			got = append(got, f.Rule)
		}
		assert.Equal(t, tt.want, got, "%s, aws %s, %d bytes", tt.version, tt.aws, tt.size)
	}
}

func TestChecksHaveRemediation(t *testing.T) {
	t.Parallel()

	for _, c := range audit.Checks() {
		assert.NotEmpty(t, c.Title, c.ID)
		assert.NotEmpty(t, c.Remediation, c.ID)
		assert.GreaterOrEqual(t, c.Severity.Rank(), 0, c.ID)
	}
	_, err := audit.ParseSeverity("urgent")
	require.Error(t, err)
}
