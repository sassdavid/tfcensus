package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
)

// runDoctorWith runs doctor for the acme-nonprod cassette in eu-west-1, with fake terragrunt and
// terraform binaries that print tgOut and tfOut.
func runDoctorWith(t *testing.T, tgOut, tfOut string, args ...string) (cliResult, string) {
	t.Helper()
	tg := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: []fakebin.Rule{
		{Args: []string{"--version"}, Stdout: tgOut},
	}})
	tf := fakebin.Install(t, "terraform", fakebin.Spec{
		Rules:     []fakebin.Rule{{Args: []string{"version"}, Stdout: tfOut}},
		RecordEnv: []string{"CHECKPOINT_DISABLE"},
	})
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{{Name: "acme-nonprod", Region: "eu-west-1"}}, "")
	var stdout, stderr bytes.Buffer
	d := &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Env: app.Env{
			"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "NO_COLOR=1", "AWS_EC2_METADATA_DISABLED=true",
		},
		Clock:         func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) },
		Getwd:         func() (string, error) { return home, nil },
		HTTPTransport: awstest.Replay(t, filepath.Join("../../testdata/cassettes/aws", "doctor-nonprod")),
	}
	args = append([]string{
		"doctor", "--profile", "acme-nonprod", "--region", "eu-west-1", "--tg-path", tg, "--tf-path", tf,
	}, args...)
	code := cli.Execute(t.Context(), d, args)
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}, tf
}

// TestDoctorToolVersions: doctor reports the terragrunt and terraform versions, names the gated
// features a version lacks, and exits 1 below a minimum after printing the whole report.
func TestDoctorToolVersions(t *testing.T) {
	t.Parallel()

	t.Run("current", func(t *testing.T) {
		t.Parallel()
		res, tf := runDoctorWith(t, "terragrunt version v1.2.1\n", "Terraform v1.10.2\non linux_amd64\n",
			"--format", "plain")
		require.Equal(t, 0, res.code, res.stderr)
		assert.Regexp(t, `(?m)^terragrunt +\S+terragrunt(\.exe)? +1\.2\.1 +1\.0\.0 +ok$`, res.stdout)
		assert.Regexp(t, `(?m)^terraform +\S+terraform(\.exe)? +1\.10\.2 +1\.5\.0 +ok$`, res.stdout)
		assert.Contains(t, res.stdout,
			"terraform 1.10.2: unavailable: resource identity (needs 1.12.0), query (needs 1.14.0)\n")
		assert.Contains(t, res.stdout, "111111111111  eu-west-1")
		calls := fakebin.Calls(t, tf)
		require.Len(t, calls, 1)
		assert.Equal(t, "1", calls[0].Env["CHECKPOINT_DISABLE"], "no checkpoint call from terraform")
	})

	t.Run("too old", func(t *testing.T) {
		t.Parallel()
		res, _ := runDoctorWith(t, "terragrunt version v0.99.1\n", "Terraform v1.4.6\n", "--format", "plain")
		assert.Equal(t, 1, res.code)
		assert.Contains(t, res.stderr,
			"terragrunt 0.99.1 is older than the minimum 1.0.0; terraform 1.4.6 is older than the minimum 1.5.0")
		assert.Regexp(t, `(?m)^terraform +\S+ +1\.4\.6 +1\.5\.0 +too old$`, res.stdout)
		assert.Contains(t, res.stdout, "111111111111  eu-west-1", "the AWS part is still printed")
	})

	t.Run("tofu", func(t *testing.T) {
		t.Parallel()
		res, _ := runDoctorWith(t, "terragrunt version v1.2.1\n", "OpenTofu v1.13.2\non linux_amd64\n",
			"--format", "json")
		require.Equal(t, 0, res.code, res.stderr)
		require.NoError(t, validateJSON("report-v1.json", []byte(res.stdout)))
		var env struct {
			Data struct {
				Tools []struct {
					Tool     string `json:"tool"`
					Version  string `json:"version"`
					Minimum  string `json:"minimum"`
					Status   string `json:"status"`
					Features []struct {
						Name      string `json:"name"`
						Available bool   `json:"available"`
					} `json:"features"`
				} `json:"tools"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.stdout), &env))
		require.Len(t, env.Data.Tools, 2)
		tofu := env.Data.Tools[1]
		assert.Equal(t, "tofu", tofu.Tool)
		assert.Equal(t, "1.13.2", tofu.Version)
		assert.Equal(t, "1.6.0", tofu.Minimum)
		assert.Equal(t, "ok", tofu.Status)
		require.NotEmpty(t, tofu.Features)
		last := tofu.Features[len(tofu.Features)-1]
		assert.Equal(t, "query", last.Name)
		assert.False(t, last.Available, "OpenTofu has no query")
	})
}
