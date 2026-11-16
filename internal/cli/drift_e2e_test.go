package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/require"
)

// envPluginCache is terraform's and tofu's provider cache. The live workflow keeps it between runs;
// without it each run downloads the aws provider into a temp directory.
const envPluginCache = "TF_PLUGIN_CACHE_DIR"

func driftStateBucket(account string) string { return "tfcensus-e2e-drift-" + account }

// TestSpec_drift_e2e: against moto, real terragrunt applies two units with the aws provider, a change made
// through the SDK shows up as drift in run mode, in ingest mode from terragrunt's own plans, and in
// full mode next to a code change. It runs once with terraform and once with tofu, each in its own
// moto account. It needs terraform, tofu, terragrunt and Docker (or TFCENSUS_MOTO_URL), and skips
// otherwise. init downloads the aws provider from the registry; AWS calls go to moto only.
func TestSpec_drift_e2e(t *testing.T) {
	t.Parallel()
	legs := []struct {
		bin, account string
	}{
		{bin: "terraform", account: "333333333333"},
		{bin: "tofu", account: "444444444444"},
	}
	paths := e2eBinaries(t, "terraform", "tofu", "terragrunt")
	endpoint := motoEndpoint(t)
	cache := os.Getenv(envPluginCache)
	if cache == "" {
		cache = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(cache, 0o750))
	// Providers listen on a unix socket in TMPDIR, and macOS caps socket paths at 104 bytes, which
	// t.TempDir and the testscript work directory exceed.
	tmp, err := os.MkdirTemp("", "tfc") //nolint:usetesting // a short path; see above
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	for _, leg := range legs {
		t.Run(leg.bin, func(t *testing.T) {
			t.Parallel()
			s3c := motoS3(endpoint, leg.account)
			_, bucketErr := s3c.CreateBucket(t.Context(), &s3.CreateBucketInput{
				Bucket: aws.String(driftStateBucket(leg.account)),
			})
			require.NoError(t, bucketErr)
			ec2c := ec2.NewFromConfig(motoConfig(endpoint, leg.account))

			p := scriptParams(t, filepath.Join(scriptDir, "spec_drift_e2e.txtar"))
			setup := p.Setup
			p.Setup = func(env *testscript.Env) error {
				if setupErr := setup(env); setupErr != nil {
					return setupErr
				}
				e2eEnv(env, endpoint, paths)
				env.Setenv("TG_TF_PATH", leg.bin)
				env.Setenv("TF", leg.bin)
				env.Setenv("ACCOUNT", leg.account)
				env.Setenv(envPluginCache, cache)
				env.Setenv("TMPDIR", tmp)
				return os.WriteFile(filepath.Join(env.WorkDir, "repo", "account.hcl"),
					fmt.Appendf(nil, "locals {\n  id = %q\n}\n", leg.account), 0o600)
			}
			p.Cmds["tagbucket"] = func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) < 2 {
					ts.Fatalf("usage: tagbucket <bucket> <key=value>...")
				}
				ts.Check(tagBucket(context.Background(), s3c, args[0], args[1:]))
			}
			p.Cmds["openport"] = func(ts *testscript.TestScript, neg bool, args []string) {
				const wantArgs = 2
				if neg || len(args) != wantArgs {
					ts.Fatalf("usage: openport <security-group-name> <port>")
				}
				port, parseErr := strconv.ParseInt(args[1], 10, 32)
				ts.Check(parseErr)
				ts.Check(openPort(context.Background(), ec2c, args[0], int32(port)))
			}
			testscript.Run(t, p)
		})
	}
}

// tagBucket replaces the tags of bucket, as someone in the console would.
func tagBucket(ctx context.Context, c *s3.Client, bucket string, pairs []string) error {
	var tags []s3types.Tag
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("tag %q is not key=value", kv)
		}
		tags = append(tags, s3types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	_, err := c.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket: aws.String(bucket), Tagging: &s3types.Tagging{TagSet: tags},
	})
	if err != nil {
		return fmt.Errorf("s3:PutBucketTagging: %w", err)
	}
	return nil
}

// openPort adds a TCP ingress rule for port to the security group named name.
func openPort(ctx context.Context, c *ec2.Client, name string, port int32) error {
	out, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("group-name"), Values: []string{name}}},
	})
	if err != nil {
		return fmt.Errorf("ec2:DescribeSecurityGroups: %w", err)
	}
	if len(out.SecurityGroups) != 1 {
		return fmt.Errorf("want one security group named %s, found %d", name, len(out.SecurityGroups))
	}
	_, err = c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: out.SecurityGroups[0].GroupId,
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"), FromPort: aws.Int32(port), ToPort: aws.Int32(port),
			IpRanges: []ec2types.IpRange{{CidrIp: aws.String("192.0.2.0/24")}},
		}},
	})
	if err != nil {
		return fmt.Errorf("ec2:AuthorizeSecurityGroupIngress: %w", err)
	}
	return nil
}
