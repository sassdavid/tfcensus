package audit

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/discovery"
)

const md5Suffix = "-md5"

// tableTarget is one DynamoDB lock table.
type tableTarget struct {
	name   string
	region string
	ref    accounts.BackendRef
	units  []string
}

func (t *tableTarget) target() string { return "dynamodb://" + t.region + "/" + t.name }

type tableResult struct {
	table    LockTable
	findings []Finding
	problems []Problem
}

// tableTargets groups the units that lock with DynamoDB by table and region.
func tableTargets(in Input) []*tableTarget {
	byKey := map[string]*tableTarget{}
	var out []*tableTarget
	for i := range in.Units {
		u := &in.Units[i]
		if u.Excluded || u.Backend == nil || u.Backend.DynamoDBTable == "" {
			continue
		}
		region := cmp.Or(u.Backend.Region, u.Target.Region)
		k := region + "/" + u.Backend.DynamoDBTable
		t := byKey[k]
		if t == nil {
			t = &tableTarget{name: u.Backend.DynamoDBTable, region: region, ref: unitRef(u)}
			byKey[k] = t
			out = append(out, t)
		}
		t.units = append(t.units, u.Repo+"/"+u.Path)
	}
	slices.SortFunc(out, func(a, b *tableTarget) int { return strings.Compare(a.target(), b.target()) })
	return out
}

// lockModeFindings flags units that lock with DynamoDB only (LK-MODE) or with both mechanisms
// (LK-BOTH).
func lockModeFindings(units []discovery.Unit) []Finding {
	var out []Finding
	for i := range units {
		u := &units[i]
		if u.Excluded || u.Backend == nil || u.Backend.Type != discovery.BackendS3 {
			continue
		}
		f := Finding{
			Target: u.Repo + "/" + u.Path, Unit: u.Repo + "/" + u.Path, Bucket: u.Backend.Bucket,
			Key: u.Backend.Key, Table: u.Backend.DynamoDBTable, Region: u.Backend.Region,
		}
		switch u.Lock {
		case discovery.LockDynamoDB:
			f.Reason = "locks with dynamodb_table " + u.Backend.DynamoDBTable + " only"
			out = append(out, newFinding(CheckLockMode, f))
		case discovery.LockBoth:
			f.Reason = "locks with both use_lockfile and dynamodb_table " + u.Backend.DynamoDBTable
			out = append(out, newFinding(CheckLockBoth, f))
		case discovery.LockLockfile, discovery.LockNone:
		}
	}
	return out
}

func (a *auditor) auditTable(ctx context.Context, t *tableTarget, listings map[string]map[string]bool) tableResult {
	out := tableResult{table: LockTable{Name: t.name, Region: t.region, Units: len(t.units)}}
	acct, region, err := a.opts.Credentials(ctx, t.ref)
	if err != nil {
		out.problems = append(out.problems, problem(t.target(), "", err))
		return out
	}
	out.table.Account = acct.ID
	c := dynamodb.NewFromConfig(acct.Config, func(o *dynamodb.Options) { o.Region = cmp.Or(t.region, region) })
	add := func(id, key, reason string) {
		out.findings = append(out.findings, newFinding(id, Finding{
			Target: t.target(), Table: t.name, Key: key, Account: acct.ID, Region: t.region, Reason: reason,
		}))
	}
	if issues, probs := a.tableSettings(ctx, c, t, &out.table); len(probs) > 0 {
		out.problems = append(out.problems, probs...)
	} else if len(issues) > 0 {
		add(CheckLockPITR, "", strings.Join(issues, "; "))
	}
	if err = a.scanLocks(ctx, c, &out, listings, add); err != nil {
		out.problems = append(out.problems, problem(t.target(), CheckLockStale, err))
	}
	return out
}

func (a *auditor) tableSettings(
	ctx context.Context, c *dynamodb.Client, t *tableTarget, lt *LockTable,
) ([]string, []Problem) {
	var issues []string
	var probs []Problem
	desc, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(t.name)})
	if err != nil {
		probs = append(probs, problem(t.target(), CheckLockPITR, err))
	} else {
		lt.Encryption = "AWS owned key"
		if sse := desc.Table.SSEDescription; sse != nil && sse.Status == ddbtypes.SSEStatusEnabled &&
			sse.SSEType == ddbtypes.SSETypeKms {
			lt.Encryption = string(ddbtypes.SSETypeKms)
		} else {
			issues = append(issues, "encrypted with an AWS owned key, not KMS")
		}
	}
	backups, err := c.DescribeContinuousBackups(
		ctx,
		&dynamodb.DescribeContinuousBackupsInput{TableName: aws.String(t.name)},
	)
	if err != nil {
		probs = append(probs, problem(t.target(), CheckLockPITR, err))
		return issues, probs
	}
	on := false
	if d := backups.ContinuousBackupsDescription; d != nil && d.PointInTimeRecoveryDescription != nil {
		on = d.PointInTimeRecoveryDescription.PointInTimeRecoveryStatus == ddbtypes.PointInTimeRecoveryStatusEnabled
	}
	lt.PITR = &on
	if !on {
		issues = append(issues, "point-in-time recovery is off")
	}
	return issues, probs
}

// lockInfo is the part of a lock item's Info that the audit reads.
type lockInfo struct {
	Created time.Time `json:"Created"`
}

// scanLocks reads every item: lock items older than the policy are stale, and
// a digest item (LockID bucket/key-md5) whose state object is gone is orphaned.
func (a *auditor) scanLocks(
	ctx context.Context, c *dynamodb.Client, out *tableResult, listings map[string]map[string]bool,
	add func(id, key, reason string),
) error {
	stale := time.Duration(a.opts.Policy.StaleLockHours) * time.Hour
	// No projection: its ExpressionAttributeNames map serializes in random order, and lock
	// items hold no state, only LockID, Info and Digest.
	p := dynamodb.NewScanPaginator(c, &dynamodb.ScanInput{TableName: aws.String(out.table.Name)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("scan lock table: %w", err)
		}
		for _, item := range page.Items {
			id := stringAttr(item["LockID"])
			if state, ok := strings.CutSuffix(id, md5Suffix); ok {
				bucket, key, _ := strings.Cut(state, "/")
				if keys := listings[bucket]; keys != nil && !keys[key] {
					add(CheckLockMD5, id, "digest for s3://"+state+", which doesn't exist")
				}
				continue
			}
			out.table.Locks++
			var info lockInfo
			if json.Unmarshal([]byte(stringAttr(item["Info"])), &info) != nil || info.Created.IsZero() {
				continue
			}
			if age := a.opts.Now.Sub(info.Created); age > stale {
				add(
					CheckLockStale,
					id,
					fmt.Sprintf("lock held for %s, over %dh", roundAge(age), a.opts.Policy.StaleLockHours),
				)
			}
		}
	}
	return nil
}

func stringAttr(v ddbtypes.AttributeValue) string {
	if s, ok := v.(*ddbtypes.AttributeValueMemberS); ok {
		return s.Value
	}
	return ""
}

// roundAge prints an age in whole hours, or days and hours past two days.
func roundAge(d time.Duration) string {
	const day = 24
	h := int(d.Hours())
	if h < 2*day {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd%dh", h/day, h%day)
}
