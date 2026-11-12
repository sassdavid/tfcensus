package cli_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// motoImage is the image the nightly live workflow runs, pinned by digest.
	motoImage = "motoserver/moto:5.2.5@sha256:59a1965d845249b04def37a0e2565f9cc6c44543cb89b68e90f28f344b3b0d1a"
	// envMotoURL points the test at a running moto instead of starting one.
	envMotoURL  = "TFCENSUS_MOTO_URL"
	e2eAccountA = "111111111111"
	e2eAccountB = "222222222222"
	e2eRegion   = "us-east-1"
	e2eLock     = "tfcensus-e2e-lock"
)

func e2eBucket(account string) string { return "tfcensus-e2e-" + account }

// motoEndpoint returns the URL of a moto server: TFCENSUS_MOTO_URL, else a container that
// testcontainers starts. Without Docker the test is skipped.
func motoEndpoint(t *testing.T) string {
	t.Helper()
	if u := os.Getenv(envMotoURL); u != "" {
		return u
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctr, err := testcontainers.Run(t.Context(), motoImage,
		testcontainers.WithExposedPorts("5000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/moto-api/").WithPort("5000/tcp").
			WithStartupTimeout(2*time.Minute)),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)
	endpoint, err := ctr.PortEndpoint(t.Context(), "5000/tcp", "http")
	require.NoError(t, err)
	return endpoint
}

// motoConfig returns SDK config for account in moto: the role tfcensus-e2e assumed from moto's
// default credentials, which moves the calls into that account.
func motoConfig(endpoint, account string) aws.Config {
	base := aws.Config{
		Region:       e2eRegion,
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	}
	cfg := base.Copy()
	cfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(sts.NewFromConfig(base),
		"arn:aws:iam::"+account+":role/tfcensus-e2e"))
	return cfg
}

func motoS3(endpoint, account string) *s3.Client {
	return s3.NewFromConfig(motoConfig(endpoint, account), func(o *s3.Options) { o.UsePathStyle = true })
}

// seedMoto creates a versioned state bucket in each account and the lock table in account A.
func seedMoto(t *testing.T, endpoint string) {
	t.Helper()
	ctx := t.Context()
	for _, acct := range []string{e2eAccountA, e2eAccountB} {
		c := motoS3(endpoint, acct)
		_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(e2eBucket(acct))})
		require.NoError(t, err)
		_, err = c.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket:                  aws.String(e2eBucket(acct)),
			VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
		})
		require.NoError(t, err)
	}
	_, err := dynamodb.NewFromConfig(motoConfig(endpoint, e2eAccountA)).CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(e2eLock),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("LockID"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{{AttributeName: aws.String("LockID"), KeyType: ddbtypes.KeyTypeHash}},
	})
	require.NoError(t, err)
}

// TestSpec_move_2: against moto with two accounts reached through AssumeRole and versioned
// buckets, real terraform and terragrunt split a unit into two units in two accounts with surgery,
// verify, apply and roll back; the states are byte-compared with the originals after rollback.
// It needs terraform, terragrunt and Docker (or TFCENSUS_MOTO_URL), and skips otherwise. No
// request leaves the machine: units use only terraform_data, which needs no provider download,
// and every proxy variable points at a closed port, which only loopback traffic bypasses.
func TestSpec_move_2(t *testing.T) {
	t.Parallel()
	var paths []string
	for _, bin := range []string{"terraform", "terragrunt"} {
		p, err := exec.LookPath(bin)
		if err != nil {
			t.Skipf("%s is not on PATH", bin)
		}
		paths = append(paths, filepath.Dir(p))
	}
	endpoint := motoEndpoint(t)
	seedMoto(t, endpoint)

	p := scriptParams(t, filepath.Join(scriptDir, "spec_move_2.txtar"))
	setup := p.Setup
	p.Setup = func(env *testscript.Env) error {
		if err := setup(env); err != nil {
			return err
		}
		env.Setenv("PATH", strings.Join(append([]string{env.Getenv("PATH")}, paths...), string(os.PathListSeparator)))
		for k, v := range map[string]string{
			"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "AWS_REGION": e2eRegion,
			"AWS_ENDPOINT_URL": endpoint, "TG_TF_PATH": "terraform",
			"CHECKPOINT_DISABLE": "1", "TF_IN_AUTOMATION": "1",
			"HTTP_PROXY": "http://127.0.0.1:9", "HTTPS_PROXY": "http://127.0.0.1:9", "NO_PROXY": "127.0.0.1,localhost",
		} {
			env.Setenv(k, v)
		}
		return nil
	}
	p.Cmds["s3get"] = func(ts *testscript.TestScript, neg bool, args []string) {
		const wantArgs = 3
		if len(args) != wantArgs {
			ts.Fatalf("usage: s3get <account> <key> <file>")
		}
		body, err := s3Get(context.Background(), endpoint, args[0], args[1])
		switch {
		case neg && err == nil:
			ts.Fatalf("s3://%s/%s exists", e2eBucket(args[0]), args[1])
		case neg:
			return
		case err != nil:
			ts.Fatalf("read s3://%s/%s: %v", e2eBucket(args[0]), args[1], err)
		}
		ts.Check(os.WriteFile(ts.MkAbs(args[2]), body, 0o600))
	}
	testscript.Run(t, p)
}

func s3Get(ctx context.Context, endpoint, account, key string) ([]byte, error) {
	out, err := motoS3(endpoint, account).GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(e2eBucket(account)), Key: aws.String(key),
	})
	if err != nil {
		if apiErr, ok := errors.AsType[smithy.APIError](err); ok && apiErr.ErrorCode() == "NoSuchKey" {
			return nil, errors.New("no such key")
		}
		return nil, err
	}
	defer func() { _ = out.Body.Close() }()
	return io.ReadAll(out.Body)
}
