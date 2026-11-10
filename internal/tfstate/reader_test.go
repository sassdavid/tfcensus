package tfstate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const (
	fleetRepo  = "tf-fleet"
	reposDir   = "../../testdata/repos"
	goldenDir  = "../../testdata/golden/tfstate"
	fleetLines = "fleet.txt"
)

// fixtureSecrets are attribute values in the fixture states that no output may contain: secrets,
// and ordinary attributes that aren't identifiers. RklYVFVSRS1TRUNSRVQ is FIXTURE-SECRET in base64.
func fixtureSecrets() []string {
	return []string{"FIXTURE-SECRET", "RklYVFVSRS1TRUNSRVQ", "10.20.0.0/16", "t3.small", "SecureString", "acme-ci"}
}

func assertNoSecrets(t *testing.T, label string, out []byte) {
	t.Helper()
	for _, s := range fixtureSecrets() {
		assert.NotContains(t, string(out), s, "%s contains an attribute value", label)
	}
}

func lastModified() time.Time { return time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC) }

func fleetFake(t testing.TB) *awstest.Fake {
	t.Helper()
	f := awstest.NewFake(t, lastModified())
	f.LoadManifest(filepath.Join(statesDir, fleetLines))
	f.Deny = []string{"acme-restricted-tfstate"}
	return f
}

func s3Client(rt http.RoundTripper, region string) *s3.Client {
	return s3.New(s3.Options{
		Region:           region,
		Credentials:      credentials.NewStaticCredentialsProvider("AKIAFAKE", "fake-secret", ""),
		HTTPClient:       &http.Client{Transport: rt},
		RetryMaxAttempts: 1,
	})
}

func fakeClients(rt http.RoundTripper) func(context.Context, *discovery.Unit) (tfstate.Client, error) {
	return func(_ context.Context, u *discovery.Unit) (tfstate.Client, error) {
		return s3Client(rt, u.Backend.Region), nil
	}
}

// fleetUnits are the units discovery finds in testdata/repos/tf-fleet.
func fleetUnits() []discovery.Unit {
	unit := func(path, bucket, region, account string) discovery.Unit {
		return discovery.Unit{
			Repo: fleetRepo, Path: path, Kind: discovery.KindTerraform,
			Backend: &discovery.Backend{
				Type: discovery.BackendS3, Bucket: bucket, Key: path + "/terraform.tfstate", Region: region,
				UseLockfile: true,
			},
			Target: discovery.Target{Account: account, Region: region},
		}
	}
	const main = "acme-tfstate-111111111111"
	units := []discovery.Unit{
		unit("app", main, "eu-west-1", "111111111111"),
		unit("edge", main, "eu-west-1", "111111111111"),
		unit("legacy", main, "eu-west-1", "111111111111"),
		unit("network", main, "eu-west-1", "111111111111"),
		unit("planned", main, "eu-west-1", "111111111111"),
		unit("restricted", "acme-restricted-tfstate", "us-east-1", "444455556666"),
		unit("secure", main, "eu-west-1", "111111111111"),
		unit("shared", "acme-shared-tfstate", "eu-central-1", "333333333333"),
		{Repo: fleetRepo, Path: "hcp", Kind: discovery.KindTerraform, Backend: &discovery.Backend{Type: "cloud"}},
	}
	return units
}

func readFleet(t testing.TB) []tfstate.UnitState {
	t.Helper()
	r := tfstate.NewReader(tfstate.Options{Client: fakeClients(fleetFake(t)), WorkDir: reposDir})
	return r.Read(t.Context(), fleetUnits())
}

func byLabel(states []tfstate.UnitState) map[string]tfstate.UnitState {
	m := map[string]tfstate.UnitState{}
	for _, s := range states {
		m[s.Path+":"+s.Workspace] = s
	}
	return m
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return append(b, '\n')
}

// TestSpec_statereader_1: the fixture states (TF 1.5 without identity, TF 1.12 with identity,
// OpenTofu encrypted, a workspace under env:/) give the golden inventory and index. The 50 MB
// state is TestLargeState.
func TestSpec_statereader_1(t *testing.T) {
	t.Parallel()

	states := readFleet(t)
	got := byLabel(states)
	require.Len(t, states, 10, "8 s3 units, a staging workspace and a cloud backend")

	network := got["network:default"]
	assert.Equal(t, tfstate.StatusOK, network.Status)
	assert.Equal(t, "1.5.7", network.TerraformVersion)
	assert.Equal(t, 7, network.Instances, "the deposed db instance isn't counted")
	assert.Equal(t, 1, network.DataSources)
	assert.Equal(t, tfstate.EngineTerraform, network.Engine)
	assert.Equal(t, ".terraform-version", network.EngineFrom)
	assert.Equal(t, lastModified(), *network.LastModified)

	app := got["app:default"]
	assert.Equal(t, []tfstate.ProviderVersion{
		{
			Source:        "hashicorp/aws",
			Aliases:       []string{"use1"},
			Version:       "6.20.0",
			VersionSource: tfstate.VersionFromLockFile,
		},
		{Source: "hashicorp/random", Version: "3.7.2", VersionSource: tfstate.VersionFromLockFile},
	}, app.Providers)
	assert.Empty(t, app.Engine, "nothing in app says which tool applied it")

	assert.Equal(t, map[string]int{"aws_cloudfront_distribution": 1, "aws_route53_record": 1},
		got["edge:default"].ResourceTypes, "workspaces don't share counts")
	staging := got["edge:staging"]
	assert.Equal(t, tfstate.StatusOK, staging.Status)
	assert.Equal(t, "env:/staging/edge/terraform.tfstate", staging.Key)
	assert.Equal(t, tfstate.EngineOpenTofu, staging.Engine)

	secure := got["secure:default"]
	assert.Equal(t, tfstate.StatusEncrypted, secure.Status)
	assert.True(t, secure.Encrypted)
	assert.Equal(t, uint64(12), *secure.Serial)
	assert.Equal(t, "state encryption", secure.EngineFrom)

	assert.Equal(t, tfstate.StatusUnsupported, got["legacy:default"].Status)
	assert.Equal(t, tfstate.StatusAbsent, got["planned:default"].Status)
	assert.Equal(t, tfstate.StatusOK, got["shared:default"].Status)
	assert.Zero(t, got["shared:default"].Instances)
	assert.Equal(t, tfstate.StatusSkipped, got["hcp:default"].Status)

	restricted := got["restricted:default"]
	assert.Equal(t, tfstate.StatusError, restricted.Status)
	require.ErrorIs(t, restricted.Err, tfstate.ErrAccessDenied)
	assert.Contains(t, restricted.Warnings[0], "workspaces not listed")

	statesJSON := marshal(t, states)
	testutil.Golden(t, filepath.Join(goldenDir, "fleet-states.json"), statesJSON)
	indexJSON := marshal(t, tfstate.BuildIndex(states))
	testutil.Golden(t, filepath.Join(goldenDir, "fleet-index.json"), indexJSON)
}

// TestSpec_statereader_2: no attribute value except identifiers reaches the inventory or the
// index. The CLI outputs are checked in the cli package.
func TestSpec_statereader_2(t *testing.T) {
	t.Parallel()

	states := readFleet(t)
	assertNoSecrets(t, "inventory", marshal(t, states))
	ix := tfstate.BuildIndex(states)
	assertNoSecrets(t, "index", marshal(t, ix))
	for _, ext := range []string{".json", ".json.gz", ".json.zst"} {
		path := filepath.Join(t.TempDir(), "index"+ext)
		require.NoError(t, tfstate.WriteIndexFile(path, ix))
		var back tfstate.Index
		require.NoError(t, tfstate.ReadIndexFile(path, &back))
		assert.Equal(t, ix.Entries, back.Entries, ext)
	}
}

// TestSpec_statereader_3: network and app both manage sg-0123456789abcdef0, so the index reports
// it as double-managed, on the ARN because only app's state has an identity.
func TestSpec_statereader_3(t *testing.T) {
	t.Parallel()

	ix := tfstate.BuildIndex(readFleet(t))
	require.Len(t, ix.DoubleManaged, 1)
	assert.Equal(t, tfstate.DoubleManaged{
		Kind: tfstate.KeyARN,
		Type: "aws_security_group",
		Key:  "arn:aws:ec2:eu-west-1:111111111111:security-group/sg-0123456789abcdef0",
		Refs: []tfstate.Ref{
			{Unit: "tf-fleet/app", Workspace: "default", Address: "aws_security_group.legacy_web"},
			{Unit: "tf-fleet/network", Workspace: "default", Address: "aws_security_group.web"},
		},
	}, ix.DoubleManaged[0])
}

func TestIndexKeys(t *testing.T) {
	t.Parallel()

	ix := tfstate.BuildIndex(readFleet(t))
	entries := map[string]tfstate.Entry{}
	for _, e := range ix.Entries {
		entries[e.Unit+" "+e.Address] = e
	}
	bucket := entries["tf-fleet/app aws_s3_bucket.assets"]
	assert.Equal(t, []tfstate.Key{
		{
			Kind:  tfstate.KeyIdentity,
			Type:  "aws_s3_bucket",
			Value: "account_id=111111111111,bucket=acme-assets-dev,region=eu-west-1",
		},
		{Kind: tfstate.KeyARN, Type: "aws_s3_bucket", Value: "arn:aws:s3:::acme-assets-dev"},
		{Kind: tfstate.KeyID, Type: "aws_s3_bucket", Value: "111111111111/eu-west-1/acme-assets-dev"},
	}, bucket.Keys())

	role := entries["tf-fleet/app aws_iam_role.app"]
	assert.Empty(t, role.Region, "IAM ARNs have no region")
	assert.Equal(t, "account_id=111111111111,name=acme-app", role.Keys()[0].Value)

	random := entries["tf-fleet/app random_password.db"]
	assert.Empty(t, random.Keys(), "random_password ids are not AWS IDs")

	cert := entries["tf-fleet/app aws_acm_certificate.cdn"]
	assert.Equal(t, "us-east-1", cert.Region)

	_, deposed := entries["tf-fleet/network aws_db_instance.main"]
	assert.True(t, deposed)
	for _, e := range ix.Entries {
		assert.NotEqual(t, "db-ZYXWVUTSRQPONMLKJIHGFEDCB0", e.ID, "deposed objects aren't indexed")
		assert.NotEqual(t, "aws_availability_zones", e.Type, "data sources aren't indexed")
	}
}

func TestReadCredentialError(t *testing.T) {
	t.Parallel()

	r := tfstate.NewReader(tfstate.Options{
		Client: func(context.Context, *discovery.Unit) (tfstate.Client, error) {
			return nil, fmt.Errorf("load backend credentials: %w", context.DeadlineExceeded)
		},
	})
	got := r.Read(t.Context(), fleetUnits()[:1])
	require.Len(t, got, 1)
	assert.Equal(t, tfstate.StatusError, got[0].Status)
	assert.ErrorIs(t, got[0].Err, context.DeadlineExceeded)
}

// largeState streams a raw state of at least size bytes to w. Every instance carries a long
// attribute value, so the parser has to skip far more than it keeps.
func largeState(w *bytes.Buffer, instance int) {
	fmt.Fprintf(w, `{"mode":"managed","type":"aws_security_group","name":"sg%d","provider":`+
		`"provider[\"registry.terraform.io/hashicorp/aws\"]","instances":[{"schema_version":1,"attributes":`+
		`{"arn":"arn:aws:ec2:eu-west-1:111111111111:security-group/sg-%017x","id":"sg-%017x",`+
		`"description":"FIXTURE-SECRET-%s","ingress":[{"cidr_blocks":["10.20.0.0/16"],"from_port":443}]}}]}`,
		instance, instance, instance, strings.Repeat("x", 1900))
}

const (
	largeSize = 50 << 20
	maxMemory = 200 << 20
)

// writeLarge writes the large state to pw in chunks, so the test never holds all of it.
func writeLarge(pw interface {
	Write(p []byte) (int, error)
	CloseWithError(err error) error
},
) {
	var buf bytes.Buffer
	written := 0
	buf.WriteString(`{"version":4,"terraform_version":"1.12.2","serial":1,"lineage":"large","resources":[`)
	for i := 0; written < largeSize; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		largeState(&buf, i)
		if buf.Len() > 1<<16 {
			n, err := pw.Write(buf.Bytes())
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			written += n
			buf.Reset()
		}
	}
	buf.WriteString(`]}`)
	_, err := pw.Write(buf.Bytes())
	_ = pw.CloseWithError(err)
}
