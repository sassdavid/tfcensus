package tfstate

import (
	"cmp"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
)

// KeyKind is the kind of a match key, in the order matching tries them.
type KeyKind string

// Key kinds.
const (
	KeyIdentity KeyKind = "identity"
	KeyARN      KeyKind = "arn"
	KeyID       KeyKind = "id"
)

// Entry is one managed resource instance in the index, with identifiers only.
type Entry struct {
	Unit      string            `json:"unit"` // repo/path
	Workspace string            `json:"workspace"`
	Address   string            `json:"address"`
	Type      string            `json:"type"`
	Provider  string            `json:"provider"`
	ID        string            `json:"id,omitempty"`
	ARN       string            `json:"arn,omitempty"`
	Identity  map[string]string `json:"identity,omitempty"`
	// IdentityAttrs are identity attribute values read from resource attributes, for states
	// without an identity block.
	IdentityAttrs map[string]string `json:"identity_attrs,omitempty"`
	RuleKeys      []string          `json:"rule_keys,omitempty"` // hashed security group rule tuples
	Account       string            `json:"account,omitempty"`   // from identity, ARN, or the unit's target
	Region        string            `json:"region,omitempty"`    // from identity, ARN, region attribute, or the unit's target
}

// AWS reports whether the entry comes from the aws or awscc provider, whose IDs are AWS resource
// IDs.
func (e *Entry) AWS() bool {
	return e.Provider == "hashicorp/aws" || e.Provider == "hashicorp/awscc"
}

// Ref points at one instance in one unit's state.
type Ref struct {
	Unit      string `json:"unit"`
	Workspace string `json:"workspace"`
	Address   string `json:"address"`
}

// DoubleManaged is a resource that more than one state manages.
type DoubleManaged struct {
	Kind KeyKind `json:"kind"`
	Type string  `json:"type"`
	Key  string  `json:"key"`
	Refs []Ref   `json:"refs"`
}

// IndexedUnit is a state in the index.
type IndexedUnit struct {
	Unit      string  `json:"unit"`
	Workspace string  `json:"workspace"`
	Bucket    string  `json:"bucket"`
	Key       string  `json:"key"`
	Status    Status  `json:"status"`
	Serial    *uint64 `json:"serial,omitempty"`
	Lineage   string  `json:"lineage,omitempty"`
	Instances int     `json:"instances"`
}

// Index lists every managed resource instance in the fleet with its identifiers. DoubleManaged is
// filled by the match package, which owns the match keys.
type Index struct {
	Units         []IndexedUnit   `json:"units"`
	Entries       []Entry         `json:"entries"`
	DoubleManaged []DoubleManaged `json:"double_managed"`
}

// BuildIndex indexes the managed, current instances of every state that was read. Data
// sources and deposed objects are left out.
func BuildIndex(states []UnitState) *Index {
	ix := &Index{Units: []IndexedUnit{}, Entries: []Entry{}}
	for i := range states {
		us := &states[i]
		if us.Status == StatusSkipped {
			continue
		}
		iu := IndexedUnit{
			Unit: us.Unit(), Workspace: us.Workspace, Bucket: us.Bucket, Key: us.Key,
			Status: us.Status, Serial: us.Serial, Lineage: us.Lineage,
		}
		if us.State != nil {
			for _, r := range us.State.Managed() {
				for _, inst := range r.Instances {
					if inst.Deposed != "" {
						continue
					}
					ix.Entries = append(ix.Entries, entry(us, &r, &inst))
					iu.Instances++
				}
			}
		}
		ix.Units = append(ix.Units, iu)
	}
	ix.DoubleManaged = []DoubleManaged{}
	return ix
}

func entry(us *UnitState, r *Resource, inst *Instance) Entry {
	e := Entry{
		Unit: us.Unit(), Workspace: us.Workspace, Address: r.Address(inst), Type: r.Type,
		Provider: r.Provider.Source, ID: inst.ID, ARN: inst.ARN, Identity: inst.Identity,
		IdentityAttrs: inst.IdentityAttrs, RuleKeys: inst.RuleKeys,
	}
	var parsed *arn.ARN
	if a, err := arn.Parse(inst.ARN); err == nil {
		parsed = &a
	}
	e.Account = cmp.Or(inst.Identity["account_id"], arnField(parsed, func(a *arn.ARN) string { return a.AccountID }))
	e.Region = cmp.Or(
		inst.Identity["region"],
		arnField(parsed, func(a *arn.ARN) string { return a.Region }),
		inst.Region,
	)
	if e.Account == "" && !globalARN(parsed) {
		e.Account = us.Target.Account
	}
	if e.Region == "" && parsed == nil {
		e.Region = us.Target.Region
	}
	return e
}

func arnField(a *arn.ARN, f func(*arn.ARN) string) string {
	if a == nil {
		return ""
	}
	return f(a)
}

// globalARN reports whether a is an ARN without an account, such as an S3 bucket's.
func globalARN(a *arn.ARN) bool { return a != nil && a.AccountID == "" }
