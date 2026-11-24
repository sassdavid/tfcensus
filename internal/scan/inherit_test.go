package scan_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/term"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/tfstate"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	inhAcct = "111111111111"
	inhRgn  = "eu-west-1"
)

// parentCase is a parent resource and what decides its category.
type parentCase struct {
	res      inventory.CloudResource
	category match.Category
}

func unmatched(r inventory.CloudResource) match.Result {
	return match.Result{Resource: r, Category: match.CategoryUnmatched, Rule: match.RuleNotInState, Reason: "no state"}
}

func (p parentCase) result() match.Result {
	res := unmatched(p.res)
	res.Category = p.category
	switch p.category {
	case match.CategoryManaged:
		res.Rule, res.ManagedBy = match.RuleARN, []tfstate.Ref{{Unit: "live/app", Address: "x.y"}}
	case match.CategoryDerived:
		res.Rule = match.RuleParent
	case match.CategoryUnmatched, match.CategoryDoubleManaged, match.CategoryUndetermined:
	}
	return res
}

func ref(r inventory.CloudResource) inventory.Ref {
	return inventory.Ref{CFNType: r.CFNType, ID: r.ID, ARN: r.ARN}
}

func child(cfn, id string, parent inventory.CloudResource, attrs map[string]string) inventory.CloudResource {
	return inventory.CloudResource{
		Account: inhAcct, Region: parent.Region, CFNType: cfn, ID: id, Attrs: attrs,
		Parents: []inventory.Ref{ref(parent)}, Sources: []string{"native"},
	}
}

func inheritEngine(t *testing.T, neverHide ...ownership.Category) scan.Sources {
	t.Helper()
	m, err := typemap.Load()
	require.NoError(t, err)
	eng, err := ownership.New(ownership.Options{
		Rules: []ownership.Rule{{
			Name: "hide-debug", Category: ownership.CategoryIgnored, Match: `resource.id.endsWith("debug")`,
			Reason: "debug policy",
		}},
		NeverHide: neverHide,
		// The aws-defaults pack would decide apex records and route tables itself; these tests are
		// about inheritance.
		Packs: map[string]bool{ownership.PackAWSDefaults: false},
	})
	require.NoError(t, err)
	return scan.Sources{Engine: eng, Map: m}
}

func cfnTag() map[string]string { return map[string]string{"aws:cloudformation:stack-name": "base"} }

func slRole() inventory.CloudResource {
	return inventory.CloudResource{
		Account: inhAcct, CFNType: "AWS::IAM::Role", ID: "AROA1", Name: "AWSServiceRoleForECS",
		ARN: "arn:aws:iam::" + inhAcct + ":role/aws-service-role/ecs.amazonaws.com/AWSServiceRoleForECS",
	}
}

// TestInherit: every child kind takes the category of an aws-managed, other-iac or derived
// parent, a child of a managed parent stays unmanaged, chains stop at depth 3, a child a user
// rule ignores stays ignored, and never_hide keeps a guarded resource's children visible.
func TestInherit(t *testing.T) {
	t.Parallel()

	user := inventory.CloudResource{
		Account: inhAcct, CFNType: "AWS::IAM::User", ID: "AIDA1", Name: "ci",
		ARN: "arn:aws:iam::" + inhAcct + ":user/ci", Tags: cfnTag(),
	}
	group := inventory.CloudResource{
		Account: inhAcct, CFNType: "AWS::IAM::Group", ID: "AGPA1", Name: "ops",
		ARN: "arn:aws:iam::" + inhAcct + ":group/ops", Tags: cfnTag(),
	}
	bucket := inventory.CloudResource{
		Account: inhAcct, Region: inhRgn, CFNType: "AWS::S3::Bucket", ID: "state", Name: "state",
		ARN: "arn:aws:s3:::state",
	}
	mBucket := bucket
	mBucket.ID, mBucket.ARN = "app", "arn:aws:s3:::app"
	sg := inventory.CloudResource{
		Account: inhAcct, Region: inhRgn, CFNType: "AWS::EC2::SecurityGroup", ID: "sg-1", Name: "eks-cluster-sg-x",
		ARN: "arn:aws:ec2:" + inhRgn + ":" + inhAcct + ":security-group/sg-1", Tags: cfnTag(),
	}
	zone := inventory.CloudResource{
		Account: inhAcct, CFNType: "AWS::Route53::HostedZone", ID: "Z1",
		ARN: "arn:aws:route53:::hostedzone/Z1", Tags: cfnTag(),
	}
	nat := inventory.CloudResource{
		Account: inhAcct, Region: inhRgn, CFNType: "AWS::EC2::NatGateway", ID: "nat-1",
		ARN: "arn:aws:ec2:" + inhRgn + ":" + inhAcct + ":natgateway/nat-1",
	}
	lattice := func(id string) inventory.CloudResource {
		return inventory.CloudResource{
			Account: inhAcct, Region: inhRgn, CFNType: "AWS::VpcLattice::ResourceConfiguration", ID: id,
			ARN:   "arn:aws:vpc-lattice:" + inhRgn + ":" + inhAcct + ":resourceconfiguration/" + id,
			Attrs: map[string]string{"resource_configuration_type": "CHILD"},
		}
	}
	rcARN := lattice("rcfg-arn")
	rcARN.Attrs = map[string]string{"resource_configuration_type": "ARN"}
	rcARN.Tags = cfnTag()
	chain := []inventory.CloudResource{lattice("rcfg-c1"), lattice("rcfg-c2"), lattice("rcfg-c3"), lattice("rcfg-c4")}
	for i := range chain {
		up := rcARN
		if i > 0 {
			up = chain[i-1]
		}
		chain[i].Parents = []inventory.Ref{ref(up)}
	}
	role := slRole()

	results := []match.Result{
		parentCase{role, match.CategoryUnmatched}.result(),
		parentCase{user, match.CategoryUnmatched}.result(),
		parentCase{group, match.CategoryUnmatched}.result(),
		parentCase{bucket, match.CategoryDerived}.result(),
		parentCase{mBucket, match.CategoryManaged}.result(),
		parentCase{sg, match.CategoryUnmatched}.result(),
		parentCase{zone, match.CategoryUnmatched}.result(),
		parentCase{nat, match.CategoryDerived}.result(),
		parentCase{rcARN, match.CategoryUndetermined}.result(),
	}
	want := map[string]scan.Category{}
	add := func(r inventory.CloudResource, c scan.Category) {
		results = append(results, unmatched(r))
		want[r.CFNType+" "+r.ID] = c
	}
	const awsM, iac, der, unm = scan.CategoryAWSManaged, scan.CategoryOtherIaC, scan.CategoryDerived, scan.CategoryUnmanaged
	add(child("Native::IAM::RolePolicyAttachment", "slr/a", role, nil), awsM)
	add(child("AWS::IAM::RolePolicy", "slr:inline", role, nil), awsM)
	add(child("Native::IAM::UserPolicyAttachment", "ci/a", user, nil), iac)
	add(child("AWS::IAM::UserPolicy", "ci:inline", user, nil), iac)
	add(child("Native::IAM::GroupPolicyAttachment", "ops/a", group, nil), iac)
	add(child("AWS::IAM::GroupPolicy", "ops:inline", group, nil), iac)
	add(child("AWS::S3::BucketPolicy", "state", bucket, nil), der)
	add(child("Native::S3::BucketVersioning", "state", bucket, nil), der)
	add(child("Native::S3::BucketEncryption", "state", bucket, nil), der)
	add(child("Native::S3::BucketLifecycle", "state", bucket, nil), der)
	add(child("Native::S3::BucketPublicAccessBlock", "state", bucket, nil), der)
	add(child("Native::S3::BucketLifecycle", "app", mBucket, nil), unm)
	add(child("AWS::EC2::SecurityGroupIngress", "sgr-in", sg, nil), iac)
	add(child("AWS::EC2::SecurityGroupEgress", "sgr-out", sg, nil), iac)
	add(child("AWS::Route53::RecordSet", "Z1_ns", zone, map[string]string{"apex": "true", "type": "NS"}), iac)
	add(child("AWS::Route53::RecordSet", "Z1_soa", zone, map[string]string{"apex": "true", "type": "SOA"}), iac)
	add(child("AWS::Route53::RecordSet", "Z1_www", zone, map[string]string{"apex": "false", "type": "A"}), unm)
	add(child("AWS::Route53::RecordSet", "Z1_txt", zone, map[string]string{"apex": "true", "type": "TXT"}), unm)
	add(child("AWS::EC2::RouteTable", "rtb-1", nat, nil), der)
	add(child("Native::IAM::RolePolicyAttachment", "slr/debug", role, nil), scan.CategoryIgnored)
	add(chain[0], iac)
	add(chain[1], iac)
	add(chain[2], iac)
	results[len(results)-1].Category, results[len(results)-1].Rule = match.CategoryUndetermined, match.RuleNoMapping
	add(chain[3], unm)

	findings := scan.Classify(results, inheritEngine(t))
	for i := range findings {
		f := &findings[i]
		c, ok := want[f.CFNType+" "+f.ID]
		if !ok {
			continue
		}
		assert.Equal(t, c, f.Category, "%s %s: %s (%s)", f.CFNType, f.ID, f.Reason, f.Rule)
		if c == awsM || c == iac || c == der {
			assert.Equal(t, scan.RuleInherit+string(c), f.Rule, f.ID)
			require.NotNil(t, f.Parent, f.ID)
			assert.Contains(t, f.Reason, f.Parent.Key, f.ID)
		}
	}
	last := findings[len(findings)-2]
	require.NotNil(t, last.Parent)
	require.NotNil(t, last.Parent.Parent)
	require.NotNil(t, last.Parent.Parent.Parent, "depth 3 keeps the whole chain")
	assert.Equal(t, rcARN.ARN, last.Parent.Parent.Parent.Key)
	assert.Equal(t, iac, last.Category, "an undetermined child inherits too")
	assert.Equal(t, "aws", findings[9].Owner, "the parent's owner")

	guarded := scan.Classify(results, inheritEngine(t, ownership.CategoryOtherIaC))
	for i := range guarded {
		f := &guarded[i]
		switch f.ID {
		case "ci/a", "ci:inline":
			assert.Equal(t, unm, f.Category, "never_hide keeps the guarded user's policies visible")
		case "ops/a", "sgr-in":
			assert.Equal(t, iac, f.Category, "never_hide guards only guarded resources")
		}
	}
}

// TestExplainInherit: --explain prints the parent chain.
func TestExplainInherit(t *testing.T) {
	t.Parallel()
	role := slRole()
	inline := child("AWS::IAM::RolePolicy", "AWSServiceRoleForECS:extra", role, map[string]string{
		"role": "AWSServiceRoleForECS", "name": "extra",
	})
	inline.Name = "extra"
	results := []match.Result{unmatched(role), unmatched(inline)}
	ex := scan.Explain(results, 1, inheritEngine(t))
	require.NotNil(t, ex.Finding.Parent)
	var b bytes.Buffer
	require.NoError(t, scan.WriteExplanation(term.Writer(&b, term.Stream{}.Profile()), &ex))
	testutil.Golden(t, filepath.Join(goldenDir, "explain-inherit.golden"), b.Bytes())
}
