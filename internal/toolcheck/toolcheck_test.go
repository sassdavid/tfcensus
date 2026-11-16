package toolcheck_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/toolcheck"
)

// stub answers each binary with fixed output, or an error.
type stub map[string]any

func (s stub) Run(_ context.Context, c execx.Cmd) (*execx.Result, error) {
	switch v := s[c.Name].(type) {
	case string:
		return &execx.Result{Stdout: []byte(v)}, nil
	case error:
		return nil, v
	}
	return nil, fmt.Errorf("%w: %q not found on PATH", execx.ErrNotFound, c.Name)
}

func TestCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		tfPath   string
		bins     stub
		tg, tf   toolcheck.Status
		tfKind   toolcheck.Kind
		tfVer    string
		features []bool
	}{
		{
			name:   "current terraform",
			tfPath: "terraform",
			bins: stub{
				"terragrunt": "terragrunt version v1.2.1\n",
				"terraform":  "Terraform v1.17.1\non darwin_arm64\n",
			},
			tg: toolcheck.StatusOK, tf: toolcheck.StatusOK, tfKind: toolcheck.KindTerraform, tfVer: "1.17.1",
			features: []bool{true, true, true, true},
		},
		{
			name:   "terraform at the floor",
			tfPath: "terraform",
			bins:   stub{"terragrunt": "terragrunt version 1.0.0", "terraform": "Terraform v1.5.7\n"},
			tg:     toolcheck.StatusOK, tf: toolcheck.StatusOK, tfKind: toolcheck.KindTerraform, tfVer: "1.5.7",
			features: []bool{false, false, false, false},
		},
		{
			name:   "too old",
			tfPath: "terraform",
			bins:   stub{"terragrunt": "terragrunt version v0.99.0", "terraform": "Terraform v1.4.6\n"},
			tg:     toolcheck.StatusTooOld, tf: toolcheck.StatusTooOld, tfKind: toolcheck.KindTerraform, tfVer: "1.4.6",
			features: []bool{false, false, false, false},
		},
		{
			name:   "tofu behind a terraform name",
			tfPath: "terraform",
			bins:   stub{"terragrunt": "terragrunt version v1.2.1", "terraform": "OpenTofu v1.10.0\non linux_amd64\n"},
			tg:     toolcheck.StatusOK, tf: toolcheck.StatusOK, tfKind: toolcheck.KindTofu, tfVer: "1.10.0",
			features: []bool{true, true, false, false},
		},
		{
			name:   "old tofu",
			tfPath: "/opt/bin/tofu",
			bins:   stub{"terragrunt": "terragrunt version v1.2.1", "/opt/bin/tofu": "OpenTofu v1.5.0\n"},
			tg:     toolcheck.StatusOK, tf: toolcheck.StatusTooOld, tfKind: toolcheck.KindTofu, tfVer: "1.5.0",
			features: []bool{false, false, false, false},
		},
		{
			name:   "prerelease counts as its release",
			tfPath: "terraform",
			bins:   stub{"terragrunt": "terragrunt version v1.0.0-rc1", "terraform": "Terraform v1.12.0-beta2\n"},
			tg:     toolcheck.StatusOK, tf: toolcheck.StatusOK, tfKind: toolcheck.KindTerraform, tfVer: "1.12.0-beta2",
			features: []bool{true, true, true, false},
		},
		{
			name:   "not installed",
			tfPath: "tofu",
			bins:   stub{},
			tg:     toolcheck.StatusNotFound, tf: toolcheck.StatusNotFound, tfKind: toolcheck.KindTofu,
		},
		{
			name:   "unreadable output and a failing run",
			tfPath: "terraform",
			bins:   stub{"terragrunt": "something else", "terraform": errors.New("terraform exited 1")},
			tg:     toolcheck.StatusError, tf: toolcheck.StatusError, tfKind: toolcheck.KindTerraform,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toolcheck.Check(t.Context(), tt.bins, "terragrunt", tt.tfPath)
			assert.Equal(t, tt.tg, got[0].Status, "terragrunt: %s", got[0].Error)
			assert.Equal(t, tt.tf, got[1].Status, "terraform: %s", got[1].Error)
			assert.Equal(t, tt.tfKind, got[1].Tool)
			assert.Equal(t, tt.tfVer, got[1].Version)
			assert.Equal(t, tt.tfPath, got[1].Path)
			available := make([]bool, 0, len(got[1].Features))
			for _, f := range got[1].Features {
				available = append(available, f.Available)
			}
			if tt.features == nil {
				assert.Empty(t, available)
			} else {
				assert.Equal(t, tt.features, available)
			}
		})
	}
}
