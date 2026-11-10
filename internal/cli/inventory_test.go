package cli_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const (
	inventoryGolden = "../../testdata/golden/inventory"
	fleetCassette   = cassetteDir + "/aws/inventory-fleet"
	fleetManifest   = "../../testdata/states/fleet.txt"
)

// fleetAWSConfig gives the default credentials account 111111111111 and the acme-audit profile,
// which the restricted unit's backend names, account 444455556666.
const fleetAWSConfig = `[default]
region = eu-west-1
aws_access_key_id = AKIAFAKEDEFAULT
aws_secret_access_key = fake-default-secret

[profile acme-audit]
region = us-east-1
aws_access_key_id = AKIAFAKEAUDIT
aws_secret_access_key = fake-audit-secret
`

func fleetHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".aws"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".aws", "config"), []byte(fleetAWSConfig), 0o600))
	return home
}

func runInventory(t *testing.T, rt http.RoundTripper, args ...string) cliResult {
	t.Helper()
	cwd, err := filepath.Abs(reposDir)
	require.NoError(t, err)
	home := fleetHome(t)
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
		Getwd:         func() (string, error) { return cwd, nil },
		HTTPTransport: rt,
	}
	code := cli.Execute(t.Context(), d, append([]string{"inventory", "tf-fleet"}, args...))
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// TestInventoryCassette writes the inventory-fleet cassette from an in-process S3 (gofakes3)
// and STS, through the awstest scrubber, when golden files are updated. It runs before the
// parallel tests that replay the cassette. One state at a time keeps the recording order stable.
func TestInventoryCassette(t *testing.T) { //nolint:paralleltest // writes the cassette the parallel tests read
	fake := awstest.NewFake(t, time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC))
	fake.LoadManifest(fleetManifest)
	fake.Deny = []string{"acme-restricted-tfstate"}
	fake.Keys = map[string]string{"AKIAFAKEDEFAULT": "111111111111", "AKIAFAKEAUDIT": "444455556666"}
	rt := awstest.WriteCassette(t, fleetCassette, fake)
	if rt == nil {
		t.Skip("replayed by the inventory tests; run mise run test:golden -- -update to rewrite it")
	}
	res := runInventory(t, rt, "--concurrency", "1", "--format", "json")
	require.Equal(t, cli.ExitError, res.code, res.stderr)
}

// TestInventoryGolden checks every output format of inventory over the tf-fleet fixtures, and
// the saved index.
func TestInventoryGolden(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"json", "tty", "md", "csv"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			args := []string{"--format", format}
			var index string
			if format == "json" {
				index = filepath.Join(t.TempDir(), "index.json.zst")
				args = append(args, "--save-index", index)
			}
			res := runInventory(t, awstest.Replay(t, fleetCassette), args...)
			require.Equal(t, cli.ExitError, res.code, "the restricted unit fails: %s", res.stderr)
			assert.Contains(t, res.stderr, "read state: 1 of 9 failed")
			ext := map[string]string{"json": "json", "tty": "txt", "md": "md", "csv": "csv"}[format]
			out := []byte(res.stdout)
			if format == "json" {
				require.NoError(t, validateJSON("inventory-v1.json", out))
				require.NoError(t, validateJSON("report-v1.json", out))
				out = stableJSON(res.stdout)
			}
			testutil.Golden(t, filepath.Join(inventoryGolden, "tf-fleet."+ext), out)
			if index == "" {
				return
			}
			var env map[string]any
			require.NoError(t, tfstate.ReadIndexFile(index, &env))
			plain := filepath.Join(t.TempDir(), "index.json")
			require.NoError(t, tfstate.WriteIndexFile(plain, env))
			b, err := os.ReadFile(plain)
			require.NoError(t, err)
			require.NoError(t, validateJSON("index-v1.json", b))
		})
	}
}

// TestInventoryAttributeValues greps every inventory output, the debug log and the saved index
// in each compression for attribute values of the fixture states. Only identifiers may appear.
func TestInventoryAttributeValues(t *testing.T) {
	t.Parallel()

	secrets := []string{"FIXTURE-SECRET", "RklYVFVSRS1TRUNSRVQ", "10.20.0.0/16", "t3.small", "SecureString", "acme-ci"}
	dir := t.TempDir()
	outputs := map[string]string{}
	for _, format := range []string{"json", "tty", "plain", "md", "csv"} {
		args := []string{"--format", format, "-vv"}
		if format == "json" {
			for _, ext := range []string{".json", ".json.gz", ".json.zst"} {
				args = append(args, "--save-index", filepath.Join(dir, "index"+ext))
				res := runInventory(t, awstest.Replay(t, fleetCassette), args...)
				outputs["stdout "+format+ext] = res.stdout
				outputs["stderr "+format+ext] = res.stderr
				var env map[string]any
				require.NoError(t, tfstate.ReadIndexFile(filepath.Join(dir, "index"+ext), &env))
				raw, err := os.ReadFile(filepath.Join(dir, "index"+ext))
				require.NoError(t, err)
				plain := filepath.Join(dir, "plain"+ext+".json")
				require.NoError(t, tfstate.WriteIndexFile(plain, env))
				decoded, err := os.ReadFile(plain)
				require.NoError(t, err)
				outputs["index "+ext] = string(decoded)
				outputs["index raw "+ext] = string(raw)
				args = args[:3]
			}
			continue
		}
		res := runInventory(t, awstest.Replay(t, fleetCassette), args...)
		outputs["stdout "+format], outputs["stderr "+format] = res.stdout, res.stderr
	}
	assert.Contains(t, outputs["index .json"], "sg-0123456789abcdef0", "identifiers are kept")
	for label, out := range outputs {
		for _, s := range secrets {
			assert.NotContains(t, out, s, "%s contains an attribute value", label)
		}
	}
}

func TestInventoryUsage(t *testing.T) {
	t.Parallel()

	res := runInventory(t, nil, "--format", "sarif")
	assert.Equal(t, cli.ExitUsage, res.code)
	res = runInventory(t, nil, "--concurrency", "0")
	assert.Equal(t, cli.ExitUsage, res.code)
	assert.Contains(t, res.stderr, "--concurrency must be at least 1")
}
