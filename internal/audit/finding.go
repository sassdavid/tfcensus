package audit

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Severity ranks a finding.
type Severity string

// Severities, from least to most severe.
const (
	SeverityInfo   Severity = "info"
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// Rank orders severities: info is 0, high is 3, and anything else is -1.
func (s Severity) Rank() int {
	return slices.Index([]Severity{SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh}, s)
}

// ParseSeverity validates a --fail-on value.
func ParseSeverity(s string) (Severity, error) {
	if sev := Severity(s); sev.Rank() >= 0 {
		return sev, nil
	}
	return "", fmt.Errorf("invalid severity %q: want info, low, medium or high", s)
}

// Check IDs, as in the backend audit spec.
const (
	CheckS3Versioning  = "S3-VER"
	CheckS3MFADelete   = "S3-MFA"
	CheckS3Encryption  = "S3-ENC"
	CheckS3KeyPolicy   = "S3-KMSPOL"
	CheckS3PublicBlock = "S3-PAB"
	CheckS3TLS         = "S3-TLS"
	CheckS3Ownership   = "S3-OWN"
	CheckS3Replication = "S3-REPL"
	CheckS3Lifecycle   = "S3-LC"
	CheckS3Logging     = "S3-LOG"
	CheckS3Legacy      = "S3-LEGACY"
	CheckLockMode      = "LK-MODE"
	CheckLockBoth      = "LK-BOTH"
	CheckLockPITR      = "LK-PITR"
	CheckLockStale     = "LK-STALE"
	CheckLockMD5       = "LK-MD5"
	CheckOrphan        = "FL-ORPHAN"
	CheckAbsent        = "FL-ABSENT"
	CheckEmpty         = "FL-EMPTY"
	CheckLineage       = "FL-LINEAGE"
	CheckDouble        = "FL-DOUBLE"
	CheckTFVersion     = "FL-TFVER"
	CheckProvider      = "FL-PROV"
	CheckSize          = "FL-SIZE"
	CheckSecret        = "FL-SECRET"
)

// Check describes one audit check.
type Check struct {
	ID          string   `json:"id"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
	Remediation string   `json:"remediation"`
}

// Checks returns every check in spec order.
func Checks() []Check {
	return slices.Concat(bucketChecks(), lockChecks(), fleetChecks())
}

func bucketChecks() []Check {
	return []Check{
		{
			CheckS3Versioning, SeverityHigh, "State bucket versioning is enabled",
			"Enable versioning, so an overwritten or deleted state can be restored.",
		},
		{
			CheckS3MFADelete, SeverityLow, "State bucket has MFA delete",
			"Informational: MFA delete needs the root user's MFA device and is rarely practical.",
		},
		{
			CheckS3Encryption,
			SeverityMedium,
			"State bucket encrypts with a customer-managed KMS key",
			"Set default encryption to SSE-KMS with a customer-managed key, so key policy controls who can decrypt state.",
		},
		{
			CheckS3KeyPolicy, SeverityHigh, "State key policy doesn't let every principal decrypt",
			"Remove kms:Decrypt for Principal \"*\", or restrict it with aws:PrincipalAccount or aws:PrincipalOrgID.",
		},
		{
			CheckS3PublicBlock, SeverityHigh, "Bucket and account public access block are all on",
			"Turn on all four public access block settings on the bucket and the account.",
		},
		{
			CheckS3TLS,
			SeverityHigh,
			"State bucket policy denies requests without TLS",
			"Add a Deny for Principal \"*\", Action s3:*, the bucket and bucket/* resources, with Bool aws:SecureTransport false as the only condition.",
		},
		{
			CheckS3Ownership, SeverityMedium, "Object ownership is BucketOwnerEnforced",
			"Set object ownership to BucketOwnerEnforced, which turns ACLs off.",
		},
		{
			CheckS3Replication, SeverityMedium, "State is replicated to another region",
			"Configure cross-region replication to a versioned, KMS-encrypted bucket.",
		},
		{
			CheckS3Lifecycle, SeverityMedium, "Noncurrent state versions expire after a bounded time",
			"Add a lifecycle rule with NoncurrentVersionExpiration of at least the policy's noncurrent_days.",
		},
		{
			CheckS3Logging, SeverityLow, "State access is logged",
			"Turn on server access logging or CloudTrail S3 data events for the bucket.",
		},
		{
			CheckS3Legacy,
			SeverityLow,
			"No legacy RootAccess statement",
			"Remove the RootAccess statement that terragrunt before 1.2 added; the account root has access through IAM.",
		},
	}
}

func lockChecks() []Check {
	return []Check{
		{
			CheckLockMode,
			SeverityLow,
			"Unit locks with S3 lock files",
			"Set use_lockfile = true: dynamodb_table is deprecated since Terraform 1.11. Keep both during the migration.",
		},
		{
			CheckLockBoth,
			SeverityInfo,
			"Unit uses one lock mechanism",
			"Both lock files and DynamoDB are configured, which is fine while migrating. Remove dynamodb_table afterwards.",
		},
		{
			CheckLockPITR, SeverityMedium, "Lock table has point-in-time recovery and KMS encryption",
			"Turn on point-in-time recovery and encrypt the table with a KMS key.",
		},
		{
			CheckLockStale, SeverityHigh, "No stale locks",
			"Check that no run holds the lock, then release it with terraform force-unlock.",
		},
		{
			CheckLockMD5, SeverityLow, "No orphaned digest items",
			"Delete the -md5 item: its state object no longer exists.",
		},
	}
}

func fleetChecks() []Check {
	return []Check{
		{
			CheckOrphan, SeverityMedium, "Every state key belongs to a unit",
			"Delete or archive the state, or add the unit that owns it to the scanned repos.",
		},
		{
			CheckAbsent, SeverityLow, "Every unit's state exists",
			"Apply the unit, or fix its backend key.",
		},
		{
			CheckEmpty, SeverityLow, "State manages resources",
			"Destroy the unit's code and state, or check that the key is right.",
		},
		{
			CheckLineage, SeverityHigh, "Each lineage lives under one key",
			"One of the states is a copy. Keep the one the unit applies to and archive the other.",
		},
		{
			CheckDouble, SeverityHigh, "Each resource is managed by one state",
			"Remove the resource from all but one state with terraform state rm, or a removed block.",
		},
		{
			CheckTFVersion, SeverityMedium, "State was written by a supported version",
			"Upgrade the unit's terraform or tofu to at least the policy's min_terraform_version.",
		},
		{
			CheckProvider, SeverityLow, "Provider versions meet the policy",
			"Upgrade the provider in .terraform.lock.hcl with terraform init -upgrade.",
		},
		{
			CheckSize, SeverityLow, "State is small enough for fast plans",
			"Split the unit.",
		},
		{
			CheckSecret, SeverityInfo, "No secrets stored in plain state",
			"Use write-only arguments or ephemeral resources, or keep the secret in a secrets manager.",
		},
	}
}

// CheckByID returns the check with id.
func CheckByID(id string) (Check, bool) {
	i := slices.IndexFunc(Checks(), func(c Check) bool { return c.ID == id })
	if i < 0 {
		return Check{}, false
	}
	return Checks()[i], true
}

// Finding is one failed check on one target. It carries identifiers only: bucket names, keys,
// unit paths, resource addresses and attribute names, never attribute values.
type Finding struct {
	Rule        string      `json:"rule"` // the check ID
	Severity    Severity    `json:"severity"`
	Target      string      `json:"target"` // s3://bucket[/key], dynamodb://region/table, or repo/path[:workspace]
	Bucket      string      `json:"bucket,omitempty"`
	Table       string      `json:"table,omitempty"`
	Key         string      `json:"key,omitempty"` // state key, lock ID or lock object key
	Unit        string      `json:"unit,omitempty"`
	Account     string      `json:"account,omitempty"`
	Region      string      `json:"region,omitempty"`
	Reason      string      `json:"reason"`
	Remediation string      `json:"remediation"`
	Suppressed  *Suppressed `json:"suppressed,omitempty"`
}

// Suppressed says which suppression hid a finding.
type Suppressed struct {
	Reason  string `json:"reason"`
	Expires string `json:"expires,omitempty"`
}

func newFinding(id string, f Finding) Finding {
	c, ok := CheckByID(id)
	if !ok {
		panic("audit: unknown check " + id)
	}
	f.Rule, f.Severity, f.Remediation = c.ID, c.Severity, c.Remediation
	return f
}

// sortFindings orders findings by severity (most severe first), then check order, then target.
func sortFindings(fs []Finding) {
	order := map[string]int{}
	for i, c := range Checks() {
		order[c.ID] = i
	}
	slices.SortStableFunc(fs, func(a, b Finding) int {
		return cmp.Or(
			cmp.Compare(b.Severity.Rank(), a.Severity.Rank()),
			cmp.Compare(order[a.Rule], order[b.Rule]),
			strings.Compare(a.Target, b.Target),
			strings.Compare(a.Key, b.Key),
			strings.Compare(a.Reason, b.Reason),
		)
	})
}
