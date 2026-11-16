package validation_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/validation"
)

// TestSpec_scan_accuracy_9_stats: a reviewed scan CSV becomes counts by category, verdict, type
// and group, with no account, ID, name or ARN in the output.
func TestSpec_scan_accuracy_9_stats(t *testing.T) {
	t.Parallel()

	f, err := os.Open("../../testdata/validation/reviewed.csv")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	s, err := validation.Read(f)
	require.NoError(t, err)
	assert.Equal(t, 8, s.Resources)
	assert.Equal(t, 5, s.ByCategory["unmanaged"])
	assert.Equal(t, 2, s.ByVerdict[validation.VerdictFP], "verdicts are case-insensitive")
	rate, ok := s.FPRate()
	require.True(t, ok)
	assert.InDelta(t, 40, rate, 0.001)
	var b bytes.Buffer
	require.NoError(t, s.Write(&b))
	for _, leak := range []string{"111111111111", "arn:", "eni-0b", "clickops", "ci-deploy", "AROA"} {
		assert.NotContains(t, b.String(), leak)
	}
	testutil.Golden(t, filepath.Join("../../testdata/golden/validation", "stats.md"), b.Bytes())
}

func TestReadErrors(t *testing.T) {
	t.Parallel()

	for name, in := range map[string]string{
		"no verdict column": "account,category,cfn_type,source_type\n1,unmanaged,AWS::S3::Bucket,\n",
		"bad verdict":       "category,cfn_type,source_type,verdict\nunmanaged,AWS::S3::Bucket,,maybe\n",
		"empty":             "",
	} {
		_, err := validation.Read(strings.NewReader(in))
		assert.Error(t, err, name)
	}
}
