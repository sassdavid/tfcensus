package audit

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/iampolicy"
)

// minNoncurrentDays is the shortest noncurrent-version retention S3-LC accepts whatever the
// policy says: under a week, a bad apply noticed late can't be rolled back.
const minNoncurrentDays = 7

const (
	stateSuffix = ".tfstate"
	lockSuffix  = ".tflock"
	sidRoot     = "RootAccess"
)

// object is one listed key.
type object struct {
	key      string
	modified time.Time
}

type bucketResult struct {
	bucket   Bucket
	findings []Finding
	problems []Problem
	keys     map[string]bool // every key in the bucket; nil when it couldn't be listed
	objects  []object
	listedAt time.Time
	// noReplication is set when the bucket has no replication configuration at all.
	noReplication bool
}

// bucketJob audits one bucket with one client.
type bucketJob struct {
	a      *auditor
	t      *bucketTarget
	acct   *accounts.Account
	region string
	s3     *s3.Client
	out    *bucketResult
	policy *iampolicy.Document
}

func (a *auditor) auditBucket(ctx context.Context, t *bucketTarget) bucketResult {
	out := bucketResult{listedAt: a.opts.Now, bucket: Bucket{
		Name: t.name, Region: t.region, Source: t.source, Units: len(t.units), Replication: []Replica{},
	}}
	acct, region, err := a.credentials(ctx, t)
	if err != nil {
		out.problems = append(out.problems, problem(t.target(), "", err))
		return out
	}
	out.bucket.Account, out.bucket.Region = acct.ID, region
	j := &bucketJob{a: a, t: t, acct: acct, region: region, s3: a.opts.S3(acct, region), out: &out}
	if err = j.list(ctx); err != nil {
		out.problems = append(out.problems, problem(t.target(), "", err))
		if apiCode(err) == "NoSuchBucket" {
			return out
		}
	}
	for _, check := range []func(context.Context) error{
		j.versioning, j.encryption, j.publicAccess, j.bucketPolicy, j.ownership, j.replication,
		j.lifecycle, j.logging,
	} {
		if ctx.Err() != nil {
			break
		}
		_ = check(ctx)
	}
	return out
}

func (t *bucketTarget) target() string { return "s3://" + t.name }

func (j *bucketJob) add(id, reason string) {
	j.out.findings = append(j.out.findings, newFinding(id, Finding{
		Target: j.t.target(), Bucket: j.t.name, Account: j.acct.ID, Region: j.region, Reason: reason,
	}))
}

func (j *bucketJob) fail(check string, err error) error {
	j.out.problems = append(j.out.problems, problem(j.t.target(), check, err))
	return err
}

// list reads every key, for FL-ORPHAN, LK-STALE and LK-MD5.
func (j *bucketJob) list(ctx context.Context) error {
	keys := map[string]bool{}
	p := s3.NewListObjectsV2Paginator(j.s3, &s3.ListObjectsV2Input{Bucket: aws.String(j.t.name)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list bucket: %w", err)
		}
		for _, o := range page.Contents {
			k := aws.ToString(o.Key)
			keys[k] = true
			j.out.objects = append(j.out.objects, object{key: k, modified: aws.ToTime(o.LastModified)})
			if strings.HasSuffix(k, stateSuffix) {
				j.out.bucket.StateKeys++
			}
		}
	}
	j.out.keys = keys
	return nil
}

func (j *bucketJob) versioning(ctx context.Context) error {
	out, err := j.s3.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(j.t.name)})
	if err != nil {
		return j.fail(CheckS3Versioning, err)
	}
	j.out.bucket.Versioning = versioningLabel(out.Status)
	if out.Status != types.BucketVersioningStatusEnabled {
		j.add(CheckS3Versioning, "versioning is "+strings.ToLower(j.out.bucket.Versioning)+
			": an overwritten or deleted state can't be restored")
	}
	if out.MFADelete != types.MFADeleteStatusEnabled {
		j.add(CheckS3MFADelete, "MFA delete is off")
	}
	return nil
}

func versioningLabel(s types.BucketVersioningStatus) string {
	if s == "" {
		return "Off"
	}
	return string(s)
}

// sse is a bucket's default encryption.
type sse struct {
	algorithm string // AES256, aws:kms or aws:kms:dsse; empty when none is configured
	keyID     string // as configured: a key ID, key ARN, alias name or alias ARN
	manager   kmstypes.KeyManagerType
	keyARN    string
}

func (s sse) label() string {
	switch {
	case s.algorithm == "":
		return "none"
	case s.keyID == "" && strings.HasPrefix(s.algorithm, "aws:kms"):
		return s.algorithm + " (aws/s3 key)"
	case s.manager == kmstypes.KeyManagerTypeCustomer:
		return s.algorithm + " (customer-managed key)"
	case s.manager == kmstypes.KeyManagerTypeAws:
		return s.algorithm + " (AWS managed key)"
	default:
		return s.algorithm
	}
}

// problem returns why s fails S3-ENC, or "".
func (s sse) problem() string {
	switch {
	case s.algorithm == "":
		return "no default encryption"
	case s.algorithm == string(types.ServerSideEncryptionAes256):
		return "default encryption is SSE-S3: anyone with s3:GetObject can read state, no key policy applies"
	case s.keyID == "":
		return "default encryption uses the AWS managed aws/s3 key, whose policy can't be changed"
	case s.manager == kmstypes.KeyManagerTypeAws:
		return "default encryption uses an AWS managed key, whose policy can't be changed"
	}
	return ""
}

// readSSE reads a bucket's default encryption and, for a KMS key, who manages it.
func (j *bucketJob) readSSE(ctx context.Context, client *s3.Client, bucket, region string) (sse, error) {
	var s sse
	out, err := client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(bucket)})
	if apiCode(err) == "ServerSideEncryptionConfigurationNotFoundError" {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("get bucket encryption: %w", err)
	}
	if c := out.ServerSideEncryptionConfiguration; c != nil && len(c.Rules) > 0 {
		if d := c.Rules[0].ApplyServerSideEncryptionByDefault; d != nil {
			s.algorithm, s.keyID = string(d.SSEAlgorithm), aws.ToString(d.KMSMasterKeyID)
		}
	}
	if s.keyID == "" {
		return s, nil
	}
	kc := kms.NewFromConfig(j.acct.Config, func(o *kms.Options) { o.Region = keyRegion(s.keyID, region) })
	desc, err := kc.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(s.keyID)})
	if err != nil {
		return s, fmt.Errorf("describe key: %w", err)
	}
	s.manager, s.keyARN = desc.KeyMetadata.KeyManager, aws.ToString(desc.KeyMetadata.Arn)
	return s, nil
}

func keyRegion(keyID, fallback string) string {
	if a, err := arn.Parse(keyID); err == nil && a.Region != "" {
		return a.Region
	}
	return fallback
}

func (j *bucketJob) encryption(ctx context.Context) error {
	s, err := j.readSSE(ctx, j.s3, j.t.name, j.region)
	j.out.bucket.Encryption = s.label()
	if err != nil {
		return j.fail(CheckS3Encryption, err)
	}
	if p := s.problem(); p != "" {
		j.add(CheckS3Encryption, p)
	}
	if s.manager != kmstypes.KeyManagerTypeCustomer {
		return nil
	}
	kc := kms.NewFromConfig(j.acct.Config, func(o *kms.Options) { o.Region = keyRegion(s.keyARN, j.region) })
	pol, err := kc.GetKeyPolicy(
		ctx,
		&kms.GetKeyPolicyInput{KeyId: aws.String(s.keyARN), PolicyName: aws.String("default")},
	)
	if err != nil {
		return j.fail(CheckS3KeyPolicy, err)
	}
	doc, err := iampolicy.Parse([]byte(aws.ToString(pol.Policy)))
	if err != nil {
		return j.fail(CheckS3KeyPolicy, err)
	}
	if sids := DecryptGrantedToAll(doc, keyRegion(s.keyARN, j.region)); len(sids) > 0 {
		j.add(CheckS3KeyPolicy, fmt.Sprintf("key %s allows kms:Decrypt to principals in any account (statement %s)",
			s.keyARN, strings.Join(sids, ", ")))
	}
	return nil
}

// pabResult is a public access block: the four settings, all false when none is configured.
type pabResult struct {
	settings [4]bool
	err      error
}

func pabFrom(c *types.PublicAccessBlockConfiguration) [4]bool {
	if c == nil {
		return [4]bool{}
	}
	return [4]bool{
		aws.ToBool(c.BlockPublicAcls), aws.ToBool(c.IgnorePublicAcls),
		aws.ToBool(c.BlockPublicPolicy), aws.ToBool(c.RestrictPublicBuckets),
	}
}

func pabOff(s [4]bool) []string {
	names := []string{"BlockPublicAcls", "IgnorePublicAcls", "BlockPublicPolicy", "RestrictPublicBuckets"}
	var off []string
	for i, on := range s {
		if !on {
			off = append(off, names[i])
		}
	}
	return off
}

func (j *bucketJob) publicAccess(ctx context.Context) error {
	out, err := j.s3.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: aws.String(j.t.name)})
	if err != nil && apiCode(err) != "NoSuchPublicAccessBlockConfiguration" {
		return j.fail(CheckS3PublicBlock, err)
	}
	var bucket [4]bool
	if err == nil {
		bucket = pabFrom(out.PublicAccessBlockConfiguration)
	}
	account := j.a.accountPublicAccess(ctx, j.acct)
	if account.err != nil {
		return j.fail(CheckS3PublicBlock, account.err)
	}
	var parts []string
	if off := pabOff(bucket); len(off) > 0 {
		parts = append(parts, "bucket: "+strings.Join(off, ", ")+" off")
	}
	if off := pabOff(account.settings); len(off) > 0 {
		parts = append(parts, "account "+j.acct.ID+": "+strings.Join(off, ", ")+" off")
	}
	if len(parts) > 0 {
		j.add(CheckS3PublicBlock, strings.Join(parts, "; "))
	}
	return nil
}

func (j *bucketJob) bucketPolicy(ctx context.Context) error {
	out, err := j.s3.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(j.t.name)})
	if apiCode(err) == "NoSuchBucketPolicy" {
		j.add(CheckS3TLS, "the bucket has no policy, so plain HTTP requests aren't denied")
		return nil
	}
	if err != nil {
		return j.fail(CheckS3TLS, err)
	}
	doc, err := iampolicy.Parse([]byte(aws.ToString(out.Policy)))
	if err != nil {
		return j.fail(CheckS3TLS, err)
	}
	j.policy = doc
	if r := SecureTransportDenied(doc, j.t.name, j.acct.ID); !r.Denied {
		j.add(CheckS3TLS, "plain HTTP isn't denied for "+strings.Join(r.Gaps, "; "))
	}
	if slices.ContainsFunc(doc.Statements, func(s iampolicy.Statement) bool { return s.Sid == sidRoot }) {
		j.add(CheckS3Legacy, "the policy has the RootAccess statement that terragrunt before 1.2 added")
	}
	return nil
}

func (j *bucketJob) ownership(ctx context.Context) error {
	out, err := j.s3.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{Bucket: aws.String(j.t.name)})
	if apiCode(err) == "OwnershipControlsNotFoundError" {
		j.add(CheckS3Ownership, "no object ownership setting: ACLs are on")
		return nil
	}
	if err != nil {
		return j.fail(CheckS3Ownership, err)
	}
	got := "none"
	if c := out.OwnershipControls; c != nil && len(c.Rules) > 0 {
		got = string(c.Rules[0].ObjectOwnership)
	}
	if got != string(types.ObjectOwnershipBucketOwnerEnforced) {
		j.add(CheckS3Ownership, "object ownership is "+got+": ACLs are on")
	}
	return nil
}

func (j *bucketJob) replication(ctx context.Context) error {
	out, err := j.s3.GetBucketReplication(ctx, &s3.GetBucketReplicationInput{Bucket: aws.String(j.t.name)})
	if apiCode(err) == "ReplicationConfigurationNotFoundError" {
		j.out.noReplication = true
		j.add(CheckS3Replication, "no replication: losing the region loses every state")
		return nil
	}
	if err != nil {
		return j.fail(CheckS3Replication, err)
	}
	var issues []string
	crossRegion := false
	for _, rule := range out.ReplicationConfiguration.Rules {
		if rule.Destination == nil {
			continue
		}
		dest := strings.TrimPrefix(aws.ToString(rule.Destination.Bucket), "arn:aws:s3:::")
		rep := Replica{Bucket: dest, Status: string(rule.Status)}
		region, regionErr := bucketRegion(ctx, j.s3, dest)
		if regionErr != nil {
			_ = j.fail(CheckS3Replication, fmt.Errorf("replica %s: %w", dest, regionErr))
			j.out.bucket.Replication = append(j.out.bucket.Replication, rep)
			continue
		}
		rep.Region = region
		j.out.bucket.Replication = append(j.out.bucket.Replication, rep)
		if rule.Status != types.ReplicationRuleStatusEnabled {
			issues = append(issues, "the rule to "+dest+" is disabled")
			continue
		}
		if region == j.region {
			issues = append(issues, "replica "+dest+" is in the same region")
		} else {
			crossRegion = true
		}
		issues = append(issues, j.replicaIssues(ctx, dest, region)...)
	}
	if !crossRegion && len(issues) == 0 {
		issues = append(issues, "no enabled rule replicates to another region")
	}
	if len(issues) > 0 {
		j.add(CheckS3Replication, strings.Join(issues, "; "))
	}
	return nil
}

// replicaIssues checks that a replica passes S3-VER and S3-ENC.
func (j *bucketJob) replicaIssues(ctx context.Context, bucket, region string) []string {
	client := j.a.opts.S3(j.acct, region)
	var issues []string
	v, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
	if err != nil {
		_ = j.fail(CheckS3Replication, fmt.Errorf("replica %s: %w", bucket, err))
	} else if v.Status != types.BucketVersioningStatusEnabled {
		issues = append(issues, "replica "+bucket+" versioning is "+strings.ToLower(versioningLabel(v.Status)))
	}
	s, err := j.readSSE(ctx, client, bucket, region)
	if err != nil {
		_ = j.fail(CheckS3Replication, fmt.Errorf("replica %s: %w", bucket, err))
	} else if p := s.problem(); p != "" {
		issues = append(issues, "replica "+bucket+": "+p)
	}
	return issues
}

// bucketRegion returns where a bucket lives. GetBucketLocation answers "" for us-east-1 and
// "EU" for buckets made with the old eu-west-1 constraint.
func bucketRegion(ctx context.Context, client *s3.Client, bucket string) (string, error) {
	out, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(bucket)})
	if err != nil {
		return "", fmt.Errorf("get bucket location: %w", err)
	}
	switch loc := string(out.LocationConstraint); loc {
	case "":
		return "us-east-1", nil
	case "EU":
		return "eu-west-1", nil
	default:
		return loc, nil
	}
}

func (j *bucketJob) lifecycle(ctx context.Context) error {
	out, err := j.s3.GetBucketLifecycleConfiguration(ctx,
		&s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(j.t.name)})
	if err != nil && apiCode(err) != "NoSuchLifecycleConfiguration" {
		return j.fail(CheckS3Lifecycle, err)
	}
	days := -1
	if err == nil {
		days = j.noncurrentDays(out.Rules)
	}
	want := j.a.opts.Policy.NoncurrentDays
	switch {
	case days < 0:
		j.add(CheckS3Lifecycle, "no lifecycle rule expires noncurrent versions of every state: they are kept forever")
	case days < minNoncurrentDays:
		j.add(CheckS3Lifecycle, fmt.Sprintf(
			"noncurrent versions expire after %d days, under %d: a bad apply found late can't be rolled back",
			days, minNoncurrentDays))
	case days < want:
		j.add(
			CheckS3Lifecycle,
			fmt.Sprintf("noncurrent versions expire after %d days, under the policy's %d", days, want),
		)
	}
	return nil
}

// noncurrentDays returns the shortest NoncurrentDays of the enabled rules that cover every known
// state key, or -1. A rule covers the keys when it has no tag filter and its prefix (if any)
// starts each key.
func (j *bucketJob) noncurrentDays(rules []types.LifecycleRule) int {
	days := -1
	for _, r := range rules {
		if r.Status != types.ExpirationStatusEnabled || r.NoncurrentVersionExpiration == nil ||
			r.NoncurrentVersionExpiration.NoncurrentDays == nil {
			continue
		}
		prefix := aws.ToString(r.Prefix) //nolint:staticcheck // rules written before filters existed still use Prefix
		tagged := false
		if f := r.Filter; f != nil {
			prefix = aws.ToString(f.Prefix)
			tagged = f.Tag != nil
			if f.And != nil {
				prefix = aws.ToString(f.And.Prefix)
				tagged = tagged || len(f.And.Tags) > 0
			}
		}
		if tagged || !j.allKeysHavePrefix(prefix) {
			continue
		}
		if d := int(*r.NoncurrentVersionExpiration.NoncurrentDays); days < 0 || d < days {
			days = d
		}
	}
	return days
}

func (j *bucketJob) allKeysHavePrefix(prefix string) bool {
	for k := range j.t.known {
		if !strings.HasPrefix(k, prefix) {
			return false
		}
	}
	for k := range j.out.keys {
		if strings.HasSuffix(k, stateSuffix) && !strings.HasPrefix(k, prefix) {
			return false
		}
	}
	return true
}

func (j *bucketJob) logging(ctx context.Context) error {
	out, err := j.s3.GetBucketLogging(ctx, &s3.GetBucketLoggingInput{Bucket: aws.String(j.t.name)})
	if err != nil {
		return j.fail(CheckS3Logging, err)
	}
	if out.LoggingEnabled != nil {
		return nil
	}
	trails := j.a.dataEventTrails(ctx, j.acct, j.region)
	if trails.err != nil {
		return j.fail(CheckS3Logging, trails.err)
	}
	if trails.covers(j.t.name) {
		return nil
	}
	j.add(CheckS3Logging, "no server access logging and no CloudTrail trail records S3 data events for the bucket")
	return nil
}
