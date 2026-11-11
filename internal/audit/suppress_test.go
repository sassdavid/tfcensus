package audit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/config"
)

func TestNewSuppressionsErrors(t *testing.T) {
	t.Parallel()

	_, err := audit.NewSuppressions([]config.Suppression{
		{Reason: "matches everything"},
		{Check: "S3-VER"},
		{Check: "S3-VER", Reason: "x", Expires: "31/12/2026"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit.suppress[0]: set check, bucket, unit or key")
	assert.Contains(t, err.Error(), "audit.suppress[1]: reason is required")
	assert.Contains(t, err.Error(), `audit.suppress[2]: expires "31/12/2026" is not a YYYY-MM-DD date`)
}

// TestSuppressionsExpiry checks the expiry boundary with an injected clock: the last day is
// included, the next instant is not, and an expired entry warns.
func TestSuppressionsExpiry(t *testing.T) {
	t.Parallel()

	s, err := audit.NewSuppressions([]config.Suppression{
		{Check: "S3-*", Bucket: "acme-*", Reason: "accepted", Expires: "2026-11-10"},
		{Unit: "live/*", Key: "*.tfstate", Reason: "migrating"},
	})
	require.NoError(t, err)
	findings := []audit.Finding{
		{Rule: "S3-MFA", Bucket: "acme-state", Target: "s3://acme-state"},
		{Rule: "FL-EMPTY", Unit: "live/app", Key: "app/terraform.tfstate", Target: "live/app"},
		{Rule: "FL-EMPTY", Unit: "other/app", Key: "app/terraform.tfstate", Target: "other/app"},
	}
	tests := []struct {
		name       string
		now        time.Time
		active     int
		suppressed int
		warnings   int
	}{
		{"before", time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), 1, 2, 0},
		{"last day", time.Date(2026, 11, 10, 23, 59, 59, 0, time.UTC), 1, 2, 0},
		{"expired", time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), 2, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			active, suppressed, warnings := s.Apply(findings, tt.now)
			assert.Len(t, active, tt.active)
			assert.Len(t, suppressed, tt.suppressed)
			assert.Len(t, warnings, tt.warnings)
			for _, f := range suppressed {
				require.NotNil(t, f.Suppressed)
				assert.NotEmpty(t, f.Suppressed.Reason)
			}
			if tt.warnings > 0 {
				assert.Equal(t, "audit.suppress[0] (check S3-*) expired on 2026-11-10 and no longer applies: "+
					"1 finding(s) reported again", warnings[0])
			}
		})
	}
}
