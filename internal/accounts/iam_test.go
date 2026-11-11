package accounts_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

const policyFile = "../../docs/iam/tfcensus-readonly.json"

// grantedElsewhere lists actions the policy leaves out on purpose, with docs/iam/README.md saying
// why: sts:AssumeRole is granted per role, for exactly the org and backend roles in use.
func grantedElsewhere() []string { return []string{"sts:AssumeRole"} }

// TestSpec_accounts_4: every AWS action the code calls in tests, taken from the recorded HTTP
// traffic, is allowed by the read-only IAM policy.
func TestSpec_accounts_4(t *testing.T) {
	t.Parallel()
	allowed := policyActions(t)
	files, err := filepath.Glob(filepath.Join(cassettes, "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	seen := map[string]bool{}
	for _, f := range files {
		c, loadErr := cassette.Load(strings.TrimSuffix(f, ".yaml"))
		require.NoError(t, loadErr, f)
		for _, i := range c.Interactions {
			action := iamAction(t, &i.Request)
			seen[action] = true
		}
	}
	actions := make([]string, 0, len(seen))
	for a := range seen {
		actions = append(actions, a)
	}
	slices.Sort(actions)
	t.Logf("actions in cassettes: %v", actions)
	for _, a := range actions {
		if slices.Contains(grantedElsewhere(), a) {
			continue
		}
		assert.True(t, slices.ContainsFunc(allowed, func(p string) bool { return actionMatches(p, a) }),
			"%s is not in %s", a, policyFile)
	}
}

func policyActions(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(policyFile)
	require.NoError(t, err)
	var doc struct {
		Statement []struct {
			Effect string   `json:"Effect"`
			Action []string `json:"Action"`
		} `json:"Statement"`
	}
	require.NoError(t, json.Unmarshal(b, &doc))
	var out []string
	for _, s := range doc.Statement {
		if s.Effect == "Allow" {
			out = append(out, s.Action...)
		}
	}
	return out
}

// iamAction names the IAM action of a request: X-Amz-Target for JSON protocols, the form Action
// for query protocols such as STS, the x-id or list-type query for S3 (bucket.s3.region hosts),
// and the path for REST protocols such as Resource Explorer.
func iamAction(t *testing.T, r *cassette.Request) string {
	t.Helper()
	u, err := url.Parse(r.URL)
	require.NoError(t, err)
	service, _, _ := strings.Cut(u.Host, ".")
	if strings.Contains(u.Host, ".s3.") {
		service = "s3"
	}
	if strings.Contains(u.Host, ".s3-control.") && strings.HasSuffix(u.Path, "/configuration/publicAccessBlock") {
		return "s3:GetAccountPublicAccessBlock"
	}
	switch target := r.Headers.Get("X-Amz-Target"); {
	case service == "s3" && u.Query().Get("list-type") == "2":
		return "s3:ListBucket"
	case service == "s3" && u.Query().Get("x-id") == "ListBuckets":
		return "s3:ListAllMyBuckets"
	case service == "s3" && u.Query().Get("x-id") == "":
		return "s3:" + s3Subresource(t, u.Query())
	case service == "s3":
		return "s3:" + u.Query().Get("x-id")
	case target != "":
		_, op, _ := strings.Cut(target, ".")
		return service + ":" + op
	case r.Form.Get("Action") != "":
		return service + ":" + r.Form.Get("Action")
	default:
		return service + ":" + strings.TrimPrefix(u.Path, "/")
	}
}

// s3Subresource names the IAM action of a bucket configuration GET such as ?versioning.
func s3Subresource(t *testing.T, q url.Values) string {
	t.Helper()
	actions := map[string]string{
		"versioning":        "GetBucketVersioning",
		"encryption":        "GetEncryptionConfiguration",
		"policy":            "GetBucketPolicy",
		"publicAccessBlock": "GetBucketPublicAccessBlock",
		"ownershipControls": "GetBucketOwnershipControls",
		"replication":       "GetReplicationConfiguration",
		"lifecycle":         "GetLifecycleConfiguration",
		"logging":           "GetBucketLogging",
		"location":          "GetBucketLocation",
	}
	for sub, action := range actions {
		if q.Has(sub) {
			return action
		}
	}
	t.Fatalf("unknown S3 request with query %v", q)
	return ""
}

// actionMatches reports whether an IAM action pattern, which may contain * and ?, matches action.
// IAM compares action names without regard to case.
func actionMatches(pattern, action string) bool {
	ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(action))
	return ok
}

func TestActionMatches(t *testing.T) {
	t.Parallel()
	assert.True(t, actionMatches("config:Describe*", "config:DescribeConfigurationRecorders"))
	assert.True(t, actionMatches("resource-explorer-2:List*", "resource-explorer-2:ListIndexes"))
	assert.False(t, actionMatches("config:Describe*", "configx:DescribeX"))
	assert.False(t, actionMatches("s3:Get*", "s3:ListBucket"))
}
