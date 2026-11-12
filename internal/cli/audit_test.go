package cli_test

import (
	"bytes"
	"encoding/json"
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
	"github.com/sassdavid/tfcensus/internal/testutil/sarifcheck"
)

const (
	auditGolden   = "../../testdata/golden/audit"
	auditCassette = cassetteDir + "/aws/audit-fleet"
	auditManifest = "../../testdata/states/audit/manifest.txt"
	auditConfig   = reposDir + "/tf-audit/tfcensus.yaml"
)

// auditNow is the test clock. The fake's objects are written an hour earlier, except the stale
// lock file.
func auditNow() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }

func runAudit(t *testing.T, rt http.RoundTripper, args ...string) cliResult {
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
		Clock:         auditNow,
		Getwd:         func() (string, error) { return cwd, nil },
		HTTPTransport: rt,
	}
	full := append([]string{"audit", "backend", "tf-audit", "--config", auditConfig, "--discover"}, args...)
	code := cli.Execute(t.Context(), d, full)
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

const (
	goodKey    = "arn:aws:kms:eu-west-1:111111111111:key/0b1c2d3e-good-4000-8000-000000000001"
	replicaKey = "arn:aws:kms:eu-central-1:111111111111:key/0b1c2d3e-repl-4000-8000-000000000002"
	openKey    = "arn:aws:kms:eu-west-1:111111111111:key/0b1c2d3e-open-4000-8000-000000000003"
)

func xmlStub(body string) awstest.Stub {
	return awstest.Stub{Body: `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + body}
}

func jsonStub(v any) awstest.Stub {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return awstest.Stub{Body: string(b)}
}

const s3NS = ` xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`

func kmsSSE(key string) awstest.Stub {
	return xmlStub(`<ServerSideEncryptionConfiguration` + s3NS + `><Rule><ApplyServerSideEncryptionByDefault>` +
		`<SSEAlgorithm>aws:kms</SSEAlgorithm><KMSMasterKeyID>` + key + `</KMSMasterKeyID>` +
		`</ApplyServerSideEncryptionByDefault><BucketKeyEnabled>true</BucketKeyEnabled></Rule></ServerSideEncryptionConfiguration>`)
}

func versioning(status, mfa string) awstest.Stub {
	return xmlStub(`<VersioningConfiguration` + s3NS + `><Status>` + status + `</Status><MfaDelete>` + mfa +
		`</MfaDelete></VersioningConfiguration>`)
}

func pab(on bool) awstest.Stub {
	v := "false"
	if on {
		v = "true"
	}
	return xmlStub(`<PublicAccessBlockConfiguration` + s3NS + `><BlockPublicAcls>` + v + `</BlockPublicAcls>` +
		`<IgnorePublicAcls>` + v + `</IgnorePublicAcls><BlockPublicPolicy>` + v + `</BlockPublicPolicy>` +
		`<RestrictPublicBuckets>` + v + `</RestrictPublicBuckets></PublicAccessBlockConfiguration>`)
}

func ownership(v string) awstest.Stub {
	return xmlStub(
		`<OwnershipControls` + s3NS + `><Rule><ObjectOwnership>` + v + `</ObjectOwnership></Rule></OwnershipControls>`,
	)
}

func replication(dest string) awstest.Stub {
	return xmlStub(`<ReplicationConfiguration` + s3NS + `><Role>arn:aws:iam::111111111111:role/s3-replication</Role>` +
		`<Rule><ID>state</ID><Priority>1</Priority><Status>Enabled</Status><Filter><Prefix></Prefix></Filter>` +
		`<DeleteMarkerReplication><Status>Disabled</Status></DeleteMarkerReplication>` +
		`<Destination><Bucket>arn:aws:s3:::` + dest + `</Bucket></Destination></Rule></ReplicationConfiguration>`)
}

func lifecycle(days string) awstest.Stub {
	return xmlStub(`<LifecycleConfiguration` + s3NS + `><Rule><ID>noncurrent</ID><Filter><Prefix></Prefix></Filter>` +
		`<Status>Enabled</Status><NoncurrentVersionExpiration><NoncurrentDays>` + days +
		`</NoncurrentDays></NoncurrentVersionExpiration></Rule></LifecycleConfiguration>`)
}

func enforcedTLS(bucket string) string {
	return `{"Version":"2012-10-17","Statement":[{"Sid":"EnforcedTLS","Effect":"Deny","Principal":{"*":["*"]},` +
		`"Action":"s3:*","Resource":["arn:aws:s3:::` + bucket + `","arn:aws:s3:::` + bucket + `/*"],` +
		`"Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`
}

// goodBucket stubs a bucket that passes every bucket check.
func goodBucket(f *awstest.Fake, bucket string) {
	s := f.Stubs
	s["s3:"+bucket+"?versioning"] = versioning("Enabled", "Enabled")
	s["s3:"+bucket+"?encryption"] = kmsSSE(goodKey)
	s["s3:"+bucket+"?publicAccessBlock"] = pab(true)
	s["s3:"+bucket+"?policy"] = awstest.Stub{Body: enforcedTLS(bucket)}
	s["s3:"+bucket+"?ownershipControls"] = ownership("BucketOwnerEnforced")
	s["s3:"+bucket+"?replication"] = replication("acme-tfstate-good-replica")
	s["s3:"+bucket+"?lifecycle"] = lifecycle("90")
	s["s3:"+bucket+"?logging"] = xmlStub(`<BucketLoggingStatus` + s3NS + `><LoggingEnabled>` +
		`<TargetBucket>acme-access-logs</TargetBucket><TargetPrefix>` + bucket + `/</TargetPrefix></LoggingEnabled></BucketLoggingStatus>`)
}

func keyMetadata(arn, manager string) awstest.Stub {
	return jsonStub(map[string]any{"KeyMetadata": map[string]any{
		"Arn": arn, "KeyId": arn[strings.LastIndex(arn, "/")+1:], "KeyManager": manager, "KeyState": "Enabled",
		"AWSAccountId": "111111111111", "Enabled": true, "KeyUsage": "ENCRYPT_DECRYPT",
	}})
}

func keyPolicy(extra string) awstest.Stub {
	p := `{"Version":"2012-10-17","Statement":[{"Sid":"Admin","Effect":"Allow",` +
		`"Principal":{"AWS":"arn:aws:iam::111111111111:root"},"Action":"kms:*","Resource":"*"}` + extra + `]}`
	return jsonStub(map[string]any{"Policy": p, "PolicyName": "default"})
}

func lockItem(id string, created time.Time) map[string]any {
	info := `{"ID":"00000000-0000-4000-8000-000000000000","Operation":"OperationTypeApply","Who":"ci@runner",` +
		`"Version":"1.12.2","Created":"` + created.Format(time.RFC3339Nano) + `","Path":"` + id + `"}`
	return map[string]any{"LockID": map[string]string{"S": id}, "Info": map[string]string{"S": info}}
}

func digestItem(id string) map[string]any {
	return map[string]any{"LockID": map[string]string{"S": id}, "Digest": map[string]string{"S": "0123456789abcdef"}}
}

// auditFake builds the fake AWS that the audit-fleet cassette is recorded from: three state
// buckets (good, bad, legacy), two replicas, a discovered bucket and two lock tables.
func auditFake(t *testing.T) *awstest.Fake {
	t.Helper()
	now := auditNow()
	f := awstest.NewFake(t, now.Add(-time.Hour))
	f.Keys = map[string]string{"AKIAFAKEDEFAULT": "111111111111", "AKIAFAKEAUDIT": "444455556666"}
	f.LoadManifest(auditManifest)
	f.PutAt("acme-audit-bad", "legacy/terraform.tfstate.tflock", []byte(`{"ID":"stale"}`), now.Add(-72*time.Hour))
	f.Put("acme-audit-good", "app/terraform.tfstate.tflock", []byte(`{"ID":"fresh"}`))
	f.CreateBucket("unrelated-logs")
	for _, b := range []string{"acme-audit-good", "acme-audit-bad", "acme-audit-legacy", "acme-tfstate-orphans", "unrelated-logs"} {
		f.Regions[b] = "eu-west-1"
	}
	f.Regions["acme-tfstate-good-replica"] = "eu-central-1"
	f.Regions["acme-audit-legacy-replica"] = "eu-west-1"
	s := f.Stubs

	goodBucket(f, "acme-audit-good")
	goodBucket(f, "acme-tfstate-orphans")
	// The replica is found by --discover: it needs no replication of its own, and its state
	// copies belong to the good bucket's units.
	goodBucket(f, "acme-tfstate-good-replica")
	delete(s, "s3:acme-tfstate-good-replica?replication")
	s["s3:acme-tfstate-good-replica?encryption"] = kmsSSE(replicaKey)
	s["kms:GetKeyPolicy:"+replicaKey] = keyPolicy("")
	s["s3control:111111111111?publicAccessBlock"] = pab(true)
	s["kms:DescribeKey:"+goodKey] = keyMetadata(goodKey, "CUSTOMER")
	s["kms:GetKeyPolicy:"+goodKey] = keyPolicy("")
	s["kms:DescribeKey:"+replicaKey] = keyMetadata(replicaKey, "CUSTOMER")

	// acme-audit-bad keeps every default: SSE-S3, no policy, no public access block, no
	// replication, lifecycle or logging.
	s["s3:acme-audit-bad?versioning"] = versioning("Suspended", "Disabled")
	s["s3:acme-audit-bad?ownershipControls"] = ownership("ObjectWriter")

	s["s3:acme-audit-legacy?versioning"] = versioning("Enabled", "Disabled")
	s["s3:acme-audit-legacy?encryption"] = kmsSSE(openKey)
	s["s3:acme-audit-legacy?publicAccessBlock"] = pab(true)
	s["s3:acme-audit-legacy?policy"] = awstest.Stub{Body: `{"Version":"2012-10-17","Statement":[` +
		`{"Sid":"RootAccess","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::111111111111:root"},"Action":"s3:*",` +
		`"Resource":["arn:aws:s3:::acme-audit-legacy","arn:aws:s3:::acme-audit-legacy/*"]},` +
		`{"Sid":"DenyInsecureWrites","Effect":"Deny","Principal":"*","Action":"s3:PutObject",` +
		`"Resource":"arn:aws:s3:::acme-audit-legacy/*","Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`}
	s["s3:acme-audit-legacy?ownershipControls"] = ownership("BucketOwnerEnforced")
	s["s3:acme-audit-legacy?replication"] = replication("acme-audit-legacy-replica")
	s["s3:acme-audit-legacy?lifecycle"] = lifecycle("3")
	s["s3:acme-audit-legacy-replica?versioning"] = versioning("Suspended", "Disabled")
	s["kms:DescribeKey:"+openKey] = keyMetadata(openKey, "CUSTOMER")
	s["kms:GetKeyPolicy:"+openKey] = keyPolicy(`,{"Sid":"AllowDecryptAnywhere","Effect":"Allow","Principal":"*",` +
		`"Action":["kms:Decrypt","kms:DescribeKey"],"Resource":"*"}`)
	s["cloudtrail:DescribeTrails"] = jsonStub(map[string]any{"trailList": []map[string]any{{
		"Name": "org-trail", "TrailARN": "arn:aws:cloudtrail:eu-west-1:111111111111:trail/org-trail",
		"HomeRegion": "eu-west-1", "IsMultiRegionTrail": true, "IsOrganizationTrail": true,
	}}})
	s["cloudtrail:GetEventSelectors:arn:aws:cloudtrail:eu-west-1:111111111111:trail/org-trail"] = jsonStub(
		map[string]any{
			"TrailARN": "arn:aws:cloudtrail:eu-west-1:111111111111:trail/org-trail",
			"AdvancedEventSelectors": []map[string]any{{"Name": "state", "FieldSelectors": []map[string]any{
				{"Field": "eventCategory", "Equals": []string{"Data"}},
				{"Field": "resources.type", "Equals": []string{"AWS::S3::Object"}},
				{"Field": "resources.ARN", "StartsWith": []string{"arn:aws:s3:::acme-audit-legacy/"}},
			}}},
		},
	)

	s["dynamodb:DescribeTable:acme-audit-locks-good"] = jsonStub(map[string]any{"Table": map[string]any{
		"TableName": "acme-audit-locks-good", "TableStatus": "ACTIVE",
		"SSEDescription": map[string]any{"Status": "ENABLED", "SSEType": "KMS", "KMSMasterKeyArn": goodKey},
	}})
	s["dynamodb:DescribeContinuousBackups:acme-audit-locks-good"] = jsonStub(map[string]any{
		"ContinuousBackupsDescription": map[string]any{
			"ContinuousBackupsStatus":        "ENABLED",
			"PointInTimeRecoveryDescription": map[string]any{"PointInTimeRecoveryStatus": "ENABLED"},
		},
	})
	s["dynamodb:Scan:acme-audit-locks-good"] = jsonStub(map[string]any{"Count": 2, "ScannedCount": 2, "Items": []any{
		lockItem("acme-audit-good/app/terraform.tfstate", now.Add(-time.Hour)),
		digestItem("acme-audit-good/app/terraform.tfstate-md5"),
	}})
	s["dynamodb:DescribeTable:acme-audit-locks-bad"] = jsonStub(map[string]any{"Table": map[string]any{
		"TableName": "acme-audit-locks-bad", "TableStatus": "ACTIVE",
	}})
	s["dynamodb:DescribeContinuousBackups:acme-audit-locks-bad"] = jsonStub(map[string]any{
		"ContinuousBackupsDescription": map[string]any{
			"ContinuousBackupsStatus":        "ENABLED",
			"PointInTimeRecoveryDescription": map[string]any{"PointInTimeRecoveryStatus": "DISABLED"},
		},
	})
	s["dynamodb:Scan:acme-audit-locks-bad"] = jsonStub(map[string]any{"Count": 3, "ScannedCount": 3, "Items": []any{
		lockItem("acme-audit-bad/legacy/terraform.tfstate", now.Add(-75*time.Hour)),
		digestItem("acme-audit-bad/legacy/terraform.tfstate-md5"),
		digestItem("acme-audit-bad/gone/terraform.tfstate-md5"),
	}})
	return f
}

// TestAuditCassette writes the audit-fleet cassette from the in-process fake when golden files
// are updated. One request at a time keeps the recording order stable.
func TestAuditCassette(t *testing.T) { //nolint:paralleltest // writes the cassette the parallel tests read
	rt := awstest.WriteCassette(t, auditCassette, auditFake(t))
	if rt == nil {
		t.Skip("replayed by the audit tests; run mise run test:golden -- -update to rewrite it")
	}
	res := runAudit(t, rt, "--concurrency", "1", "--format", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
}

// TestSpec_audit_1: every check has a positive and a negative fixture in the tf-audit fleet, and
// each finding is in the golden output of every format.
func TestSpec_audit_1(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"json", "tty", "md", "csv", "sarif"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			res := runAudit(t, awstest.Replay(t, auditCassette), "--format", format)
			require.Equal(t, cli.ExitOK, res.code, res.stderr)
			ext := map[string]string{"json": "json", "tty": "txt", "md": "md", "csv": "csv", "sarif": "sarif.json"}[format]
			out := []byte(res.stdout)
			if format == "json" {
				require.NoError(t, validateJSON("audit-v1.json", out))
				require.NoError(t, validateJSON("report-v1.json", out))
				checkCoverage(t, out)
				out = stableJSON(res.stdout)
			}
			if format == "sarif" {
				require.NoError(t, sarifcheck.Validate(out))
				out = stableSARIF(t, res.stdout)
			}
			testutil.Golden(t, filepath.Join(auditGolden, "tf-audit."+ext), out)
		})
	}
}

// checkCoverage asserts that every check fails on its positive fixture and that the negative
// fixtures (the good bucket, its lock table and the network unit) pass every check.
func checkCoverage(t *testing.T, out []byte) {
	t.Helper()
	var env struct {
		Data struct {
			Checks []struct {
				ID string `json:"id"`
			} `json:"checks"`
			Findings   []map[string]string `json:"findings"`
			Suppressed []map[string]any    `json:"suppressed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &env))
	fired := map[string]bool{}
	for _, f := range env.Data.Findings {
		fired[f["rule"]] = true
		for _, good := range []string{"s3://acme-audit-good", "dynamodb://eu-west-1/acme-audit-locks-good", "tf-audit/network"} {
			assert.False(t, strings.HasPrefix(f["target"], good),
				"negative fixture %s failed %s: %s", good, f["rule"], f["reason"])
		}
	}
	for _, f := range env.Data.Suppressed {
		fired[f["rule"].(string)] = true
	}
	require.Len(t, env.Data.Checks, 25)
	for _, c := range env.Data.Checks {
		assert.True(t, fired[c.ID], "no fixture fails %s", c.ID)
	}
}

// stableSARIF blanks the tool version, which changes with every build.
func stableSARIF(t *testing.T, s string) []byte {
	t.Helper()
	var l map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &l))
	driver := l["runs"].([]any)[0].(map[string]any)["tool"].(map[string]any)["driver"].(map[string]any)
	driver["version"] = "VERSION"
	b, err := json.MarshalIndent(l, "", "  ")
	require.NoError(t, err)
	return append(b, '\n')
}

// TestSpec_audit_3: suppressions with expires behave like ownership rules. A live entry hides
// its findings, and an expired one stops matching with a warning.
func TestSpec_audit_3(t *testing.T) {
	t.Parallel()

	res := runAudit(t, awstest.Replay(t, auditCassette), "--format", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var env struct {
		Data struct {
			Findings   []map[string]any `json:"findings"`
			Suppressed []map[string]any `json:"suppressed"`
			Warnings   []string         `json:"warnings"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &env))
	for _, f := range env.Data.Findings {
		assert.NotEqual(t, "S3-MFA", f["rule"], "the live S3-MFA suppression hides %v", f["target"])
	}
	assert.Len(t, env.Data.Suppressed, 2, "S3-MFA on acme-audit-bad and acme-audit-legacy")
	assert.Contains(t, res.stdout, `"rule": "S3-LOG",`)
	require.Len(t, env.Data.Warnings, 1)
	assert.Contains(t, env.Data.Warnings[0], "audit.suppress[1] (check S3-LOG) expired on 2026-10-31")
	assert.Contains(t, res.stderr, "expired on 2026-10-31")
}

// TestAuditAttributeValues greps every audit output, SARIF included, and the debug log for
// attribute values of the fixture states. FL-SECRET names the attributes, never their values.
func TestAuditAttributeValues(t *testing.T) {
	t.Parallel()

	secrets := []string{"FIXTURE-SECRET", "10.40.0.0/16", "t3.micro", "ci@runner", "0123456789abcdef"}
	outputs := map[string]string{}
	for _, format := range []string{"json", "tty", "plain", "md", "csv", "sarif"} {
		res := runAudit(t, awstest.Replay(t, auditCassette), "--format", format, "-vv")
		require.Equal(t, cli.ExitOK, res.code, res.stderr)
		outputs["stdout "+format], outputs["stderr "+format] = res.stdout, res.stderr
		assert.Contains(t, res.stdout, "aws_db_instance.main (password)", "%s names the attribute", format)
	}
	for label, out := range outputs {
		for _, s := range secrets {
			assert.NotContains(t, out, s, "%s contains an attribute value", label)
		}
	}
}

func TestAuditExitCodes(t *testing.T) {
	t.Parallel()

	res := runAudit(t, awstest.Replay(t, auditCassette), "--fail-on", "high", "--format", "json")
	assert.Equal(t, cli.ExitFindings, res.code, res.stderr)
	assert.Contains(t, res.stderr, "at or above high")

	for _, args := range [][]string{{"--fail-on", "urgent"}, {"--concurrency", "0"}} {
		res = runAudit(t, nil, args...)
		assert.Equal(t, cli.ExitUsage, res.code, "%v: %s", args, res.stderr)
	}
}

func TestAuditBadSuppression(t *testing.T) {
	t.Parallel()

	cfg := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("schema_version: 1\naudit:\n  suppress:\n"+
		"    - {reason: everything}\n    - {check: S3-VER, reason: x, expires: soon}\n"), 0o600))
	res := runAudit(t, nil, "--config", cfg)
	assert.Equal(t, cli.ExitUsage, res.code, res.stderr)
}
