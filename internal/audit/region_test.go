package audit_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

// TestReplicaRegion finds each replica's region with HeadBucket: from the response when the
// bucket is in the client's region, else from the x-amz-bucket-region header of the 301 or 403.
func TestReplicaRegion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	f := awstest.NewFake(t, now)
	f.CreateBucket("acme-src")
	f.Regions["acme-src"] = "eu-west-1"
	f.Regions["acme-same"] = "eu-west-1"
	f.Regions["acme-moved"] = "eu-central-1"
	f.Regions["acme-denied"] = "us-east-2"
	f.Deny = []string{"acme-denied"}
	var rules strings.Builder
	for _, dest := range []string{"acme-same", "acme-moved", "acme-denied", "acme-missing"} {
		rules.WriteString(`<Rule><ID>` + dest + `</ID><Priority>1</Priority><Status>Enabled</Status>` +
			`<Filter><Prefix></Prefix></Filter><DeleteMarkerReplication><Status>Disabled</Status></DeleteMarkerReplication>` +
			`<Destination><Bucket>arn:aws:s3:::` + dest + `</Bucket></Destination></Rule>`)
	}
	f.Stubs["s3:acme-src?replication"] = awstest.Stub{
		Body: `<ReplicationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`<Role>arn:aws:iam::111111111111:role/s3-replication</Role>` + rules.String() + `</ReplicationConfiguration>`,
	}

	acct := &accounts.Account{ID: "111111111111", Partition: "aws", Config: aws.Config{
		Region:           "eu-west-1",
		Credentials:      credentials.NewStaticCredentialsProvider("AKIAFAKE", "fake-secret", ""),
		HTTPClient:       &http.Client{Transport: f},
		RetryMaxAttempts: 1,
	}}
	rep, err := audit.Run(t.Context(), audit.Options{
		S3: func(a *accounts.Account, region string) *s3.Client {
			return s3.NewFromConfig(a.Config, func(o *s3.Options) { o.Region = region })
		},
		Discover: []string{"acme-src"},
		Accounts: []*accounts.Account{acct},
		Now:      now,
	}, audit.Input{})
	require.NoError(t, err)

	require.Len(t, rep.Buckets, 1)
	assert.Equal(t, []audit.Replica{
		{Bucket: "acme-same", Region: "eu-west-1", Status: "Enabled"},
		{Bucket: "acme-moved", Region: "eu-central-1", Status: "Enabled"},
		{Bucket: "acme-denied", Region: "us-east-2", Status: "Enabled"},
		{Bucket: "acme-missing", Status: "Enabled"},
	}, rep.Buckets[0].Replication)
	i := slices.IndexFunc(rep.Incomplete, func(p audit.Problem) bool {
		return p.Check == audit.CheckS3Replication && p.Message == "NotFound"
	})
	require.NotEqual(t, -1, i, "a replica without a region is a problem: %+v", rep.Incomplete)
	assert.ErrorContains(t, rep.Incomplete[i].Err, "replica acme-missing: head bucket:")
}
