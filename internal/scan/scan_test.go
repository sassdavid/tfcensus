package scan_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const goldenDir = "../../testdata/golden/scan"

// TestSpec_scan_2: coverage is (managed + double-managed) / (managed + double-managed +
// unmanaged); aws-managed, derived, other-iac, ignored and undetermined resources don't count.
func TestSpec_scan_2(t *testing.T) {
	t.Parallel()

	// counts builds Counts from category, count pairs.
	counts := func(pairs ...any) scan.Counts {
		c := make(scan.Counts)
		for i := 0; i+1 < len(pairs); i += 2 {
			c[pairs[i].(scan.Category)] = pairs[i+1].(int)
		}
		return c
	}
	const (
		m, d, u   = scan.CategoryManaged, scan.CategoryDoubleManaged, scan.CategoryUnmanaged
		a, dv, o  = scan.CategoryAWSManaged, scan.CategoryDerived, scan.CategoryOtherIaC
		ig, undet = scan.CategoryIgnored, scan.CategoryUndetermined
	)
	tests := []struct {
		name   string
		counts scan.Counts
		want   float64
		ok     bool
	}{
		{"nothing", counts(), 0, false},
		{"only excluded categories", counts(a, 4, dv, 3, o, 2, ig, 1, undet, 9), 0, false},
		{"all managed", counts(m, 7), 100, true},
		{"all unmanaged", counts(u, 2), 0, true},
		{"double-managed counts as in state", counts(m, 1, d, 1, u, 2), 50, true},
		{"excluded categories don't move it", counts(m, 3, u, 1, a, 40, dv, 10, o, 5, ig, 5, undet, 100), 75, true},
	}
	for _, tt := range tests {
		got, ok := tt.counts.Coverage()
		assert.Equal(t, tt.ok, ok, tt.name)
		assert.InDelta(t, tt.want, got, 0.001, tt.name)
	}

	findings := []scan.Finding{
		{Account: "111111111111", Category: scan.CategoryManaged},
		{Account: "111111111111", Category: scan.CategoryUnmanaged},
		{Account: "111111111111", Region: "eu-west-1", Category: scan.CategoryManaged},
		{Account: "111111111111", Region: "eu-west-1", Category: scan.CategoryDoubleManaged},
		{Account: "111111111111", Region: "eu-west-1", Category: scan.CategoryAWSManaged},
		{Account: "111111111111", Region: "eu-west-1", Category: scan.CategoryUndetermined},
		{Account: "222222222222", Region: "us-east-1", Category: scan.CategoryDerived},
	}
	regions, accounts := scan.Summarize(findings)
	out, err := json.MarshalIndent(map[string]any{"regions": regions, "accounts": accounts}, "", "  ")
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join(goldenDir, "summary.golden.json"), append(out, '\n'))
	require.Len(t, accounts, 2)
	require.NotNil(t, accounts[0].Coverage)
	assert.InDelta(t, 75, *accounts[0].Coverage, 0.001, "3 in state of 4")
	assert.Nil(t, accounts[1].Coverage, "derived resources alone give no coverage")
	assert.Equal(t, "global", regions[0].Region, "global sorts first")
}

func TestTypeMatcher(t *testing.T) {
	t.Parallel()

	m, err := typemap.Load()
	require.NoError(t, err)
	iam := scan.TypeMatcher{"AWS::IAM::*"}
	assert.True(t, iam.Match("AWS::IAM::Role", "", nil))
	assert.False(t, iam.Match("AWS::EC2::VPC", "", []string{"aws_vpc"}))
	assert.True(t, scan.TypeMatcher{"aws_security_group*"}.Match("AWS::EC2::SecurityGroupIngress", "",
		[]string{"aws_security_group_rule"}))
	assert.True(t, scan.TypeMatcher(nil).Match("anything", "", nil))
	assert.Contains(t, iam.CFNTypes(m), "AWS::IAM::Role")
	assert.NotContains(t, iam.CFNTypes(m), "AWS::EC2::VPC")
	assert.Subset(t, scan.TypeMatcher{"aws_iam_*"}.CFNTypes(m), iam.CFNTypes(m), "a TF glob finds the same types")
	assert.Empty(t, scan.TypeMatcher{"nothing*"}.CFNTypes(m))
}

func TestGate(t *testing.T) {
	t.Parallel()

	findings := []scan.Finding{
		{CFNType: "AWS::IAM::Role", Category: scan.CategoryUnmanaged},
		{CFNType: "AWS::EC2::VPC", Category: scan.CategoryUnmanaged},
		{CFNType: "AWS::IAM::Role", Category: scan.CategoryDoubleManaged},
		{CFNType: "AWS::IAM::Role", Category: scan.CategoryIgnored},
	}
	unmanaged := []scan.Category{scan.CategoryUnmanaged}
	assert.Equal(t, 2, scan.Gate(findings, unmanaged, nil))
	assert.Equal(t, 1, scan.Gate(findings, unmanaged, scan.TypeMatcher{"AWS::IAM::*"}))
	assert.Equal(t, 2, scan.Gate(findings, []scan.Category{scan.CategoryUnmanaged, scan.CategoryDoubleManaged},
		scan.TypeMatcher{"AWS::IAM::*"}))
	assert.Zero(t, scan.Gate(findings, nil, nil))
}
