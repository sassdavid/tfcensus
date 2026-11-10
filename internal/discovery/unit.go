package discovery

import (
	"slices"
	"strings"
)

// Kind says what produced a unit.
type Kind string

// Unit kinds.
const (
	KindTerragrunt      Kind = "terragrunt"       // a terragrunt.hcl unit
	KindTerragruntStack Kind = "terragrunt-stack" // a terragrunt.stack.hcl file
	KindTerraform       Kind = "terraform"        // a plain root module with a backend or cloud block
)

// Method says how a unit's backend was resolved.
type Method string

// Resolution methods.
const (
	MethodFastPath   Method = "fast-path"  // tfcensus evaluated the HCL itself
	MethodRender     Method = "render"     // terragrunt render --json
	MethodStatic     Method = "static"     // a literal backend block in plain Terraform
	MethodUnresolved Method = "unresolved" // see Unit.Reason
)

// Lock is the state locking a backend uses.
type Lock string

// Lock kinds. Backends other than s3 have no Lock.
const (
	LockLockfile Lock = "lockfile"
	LockDynamoDB Lock = "dynamodb"
	LockBoth     Lock = "both"
	LockNone     Lock = "none"
)

// Confidence says how sure tfcensus is about a unit's target account and region.
type Confidence string

// Confidence levels.
const (
	ConfidenceHigh   Confidence = "high"   // from the aws provider block
	ConfidenceMedium Confidence = "medium" // from inputs, locals or variable defaults
	ConfidenceLow    Confidence = "low"    // guessed from the backend, or unknown
)

// BackendS3 is the only backend type tfcensus reads state from.
const BackendS3 = "s3"

// Unit is one discovered unit or root module.
type Unit struct {
	Repo         string   `json:"repo"`
	Path         string   `json:"path"` // relative to Repo, with forward slashes
	Kind         Kind     `json:"kind"`
	Backend      *Backend `json:"backend,omitempty"`
	Lock         Lock     `json:"lock,omitempty"`
	Target       Target   `json:"target"`
	Resolution   Method   `json:"resolution,omitempty"`
	Reason       string   `json:"reason,omitempty"` // why the backend is unresolved
	Excluded     bool     `json:"excluded,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Warnings     []string `json:"warnings"`
}

// Backend is a unit's state backend. Only the fields tfcensus uses are kept.
type Backend struct {
	Type               string `json:"type"`
	Bucket             string `json:"bucket,omitempty"`
	Key                string `json:"key,omitempty"`
	Region             string `json:"region,omitempty"`
	WorkspaceKeyPrefix string `json:"workspace_key_prefix,omitempty"`
	DynamoDBTable      string `json:"dynamodb_table,omitempty"`
	UseLockfile        bool   `json:"use_lockfile,omitempty"`
	RoleARN            string `json:"role_arn,omitempty"`
	Profile            string `json:"profile,omitempty"`
}

// Target is the AWS account and region a unit's resources live in.
type Target struct {
	Account     string     `json:"account,omitempty"`
	AccountFrom string     `json:"account_from,omitempty"` // where Account was found, such as provider.allowed_account_ids
	Region      string     `json:"region,omitempty"`
	RegionFrom  string     `json:"region_from,omitempty"`
	Confidence  Confidence `json:"confidence,omitempty"`
}

// Lock returns the locking an s3 backend uses, or "" for other backends.
func (b *Backend) Lock() Lock {
	if b == nil || b.Type != BackendS3 {
		return ""
	}
	switch {
	case b.UseLockfile && b.DynamoDBTable != "":
		return LockBoth
	case b.UseLockfile:
		return LockLockfile
	case b.DynamoDBTable != "":
		return LockDynamoDB
	default:
		return LockNone
	}
}

func (u *Unit) warn(msg string) {
	if !slices.Contains(u.Warnings, msg) {
		u.Warnings = append(u.Warnings, msg)
	}
}

func (u *Unit) setBackend(b *Backend, m Method) {
	u.Backend = b
	u.Lock = b.Lock()
	u.Resolution = m
	u.Reason = ""
	if b.Type != BackendS3 {
		u.warn("unsupported backend " + b.Type + ": tfcensus reads state from s3 only")
	}
}

func (u *Unit) unresolved(reason string) {
	u.Backend = nil
	u.Lock = ""
	u.Resolution = MethodUnresolved
	u.Reason = reason
}

// sortUnits orders units by repo, then path.
func sortUnits(units []Unit) {
	slices.SortStableFunc(units, func(a, b Unit) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
}
