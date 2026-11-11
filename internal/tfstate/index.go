package tfstate

import (
	"cmp"
	"maps"
	"slices"
	"strings"

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

func keyRank(k KeyKind) int {
	return slices.Index([]KeyKind{KeyIdentity, KeyARN, KeyID}, k)
}

// Key is one way to find a managed resource instance. Every kind includes the resource type, so
// an aws_sns_topic_policy whose arn is the topic's ARN doesn't collide with the topic.
type Key struct {
	Kind  KeyKind
	Type  string
	Value string // canonical identity, the ARN, or account/region/id
}

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

// Keys returns the entry's match keys, strongest first.
func (e *Entry) Keys() []Key {
	var keys []Key
	if len(e.Identity) > 0 {
		id := maps.Clone(e.Identity)
		if _, ok := id["account_id"]; !ok && e.Account != "" {
			id["account_id"] = e.Account
		}
		if _, ok := id["region"]; !ok && e.Region != "" {
			id["region"] = e.Region
		}
		keys = append(keys, Key{Kind: KeyIdentity, Type: e.Type, Value: (&Instance{Identity: id}).IdentityString()})
	}
	if e.ARN != "" {
		keys = append(keys, Key{Kind: KeyARN, Type: e.Type, Value: e.ARN})
	}
	// Without an account an ID such as a role name says nothing about which resource it is, and
	// other providers' IDs (random_password's is always "none") aren't AWS resource IDs.
	if e.ID != "" && e.Account != "" && awsProvider(e.Provider) {
		keys = append(keys, Key{Kind: KeyID, Type: e.Type, Value: e.Account + "/" + e.Region + "/" + e.ID})
	}
	return keys
}

func awsProvider(source string) bool {
	return source == "hashicorp/aws" || source == "hashicorp/awscc"
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

// Index maps every managed resource instance in the fleet to its match keys.
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
	ix.DoubleManaged = doubleManaged(ix.Entries)
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

// doubleManaged finds keys that instances of more than one state share. When the same states
// collide on several keys, only the strongest key is reported.
func doubleManaged(entries []Entry) []DoubleManaged {
	byKey := map[Key][]Ref{}
	for i := range entries {
		e := &entries[i]
		ref := Ref{Unit: e.Unit, Workspace: e.Workspace, Address: e.Address}
		for _, k := range e.Keys() {
			byKey[k] = append(byKey[k], ref)
		}
	}
	best := map[string]DoubleManaged{}
	for k, refs := range byKey {
		if !multipleStates(refs) {
			continue
		}
		slices.SortFunc(refs, compareRefs)
		group := refsKey(refs)
		if cur, ok := best[group]; ok && keyRank(cur.Kind) <= keyRank(k.Kind) {
			continue
		}
		best[group] = DoubleManaged{Kind: k.Kind, Type: k.Type, Key: k.Value, Refs: refs}
	}
	out := slices.Collect(maps.Values(best))
	slices.SortFunc(out, func(a, b DoubleManaged) int {
		return cmp.Or(strings.Compare(a.Type, b.Type), strings.Compare(a.Key, b.Key))
	})
	if out == nil {
		out = []DoubleManaged{}
	}
	return out
}

func multipleStates(refs []Ref) bool {
	for _, r := range refs[1:] {
		if r.Unit != refs[0].Unit || r.Workspace != refs[0].Workspace {
			return true
		}
	}
	return false
}

func compareRefs(a, b Ref) int {
	return cmp.Or(strings.Compare(a.Unit, b.Unit), strings.Compare(a.Workspace, b.Workspace),
		strings.Compare(a.Address, b.Address))
}

func refsKey(refs []Ref) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.Unit + "\x00" + r.Workspace + "\x00" + r.Address
	}
	return strings.Join(parts, "\x01")
}
