package audit

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const mib = 1 << 20

// fleetFindings runs the checks that need the whole fleet: orphaned and stale lock objects from
// the bucket listings, and the state checks from the states that were read.
func fleetFindings(in Input, buckets []*bucketTarget, results []bucketResult, p config.AuditPolicy) []Finding {
	var out []Finding
	for i, t := range buckets {
		out = append(out, listingFindings(t, &results[i], p)...)
	}
	for i := range in.States {
		out = append(out, stateFindings(&in.States[i], p)...)
	}
	out = append(out, lineageFindings(in.States)...)
	if in.Index != nil {
		out = append(out, doubleFindings(in.Index.DoubleManaged)...)
	}
	return out
}

func listingFindings(t *bucketTarget, r *bucketResult, p config.AuditPolicy) []Finding {
	if r.keys == nil {
		return nil
	}
	var out []Finding
	stale := time.Duration(p.StaleLockHours) * time.Hour
	add := func(id, key, reason string) {
		out = append(out, newFinding(id, Finding{
			Target: "s3://" + t.name + "/" + key, Bucket: t.name, Key: key, Account: r.bucket.Account,
			Region: r.bucket.Region, Reason: reason,
		}))
	}
	for _, o := range r.objects {
		switch {
		case strings.HasSuffix(o.key, stateSuffix) && !t.known[o.key]:
			reason := "no discovered unit resolves to this key"
			if t.source == SourceDiscover {
				reason = "state in a discovered bucket that no unit uses"
			}
			add(CheckOrphan, o.key, reason)
		case strings.HasSuffix(o.key, lockSuffix) && !o.modified.IsZero() && r.listedAt.Sub(o.modified) > stale:
			add(CheckLockStale, o.key, fmt.Sprintf("lock file is %s old, over %dh",
				roundAge(r.listedAt.Sub(o.modified)), p.StaleLockHours))
		}
	}
	return out
}

func stateLabel(s *tfstate.UnitState) string {
	if s.Workspace == tfstate.DefaultWorkspace || s.Workspace == "" {
		return s.Unit()
	}
	return s.Unit() + ":" + s.Workspace
}

func stateFinding(id string, s *tfstate.UnitState, reason string) Finding {
	return newFinding(id, Finding{
		Target: stateLabel(s), Unit: s.Unit(), Bucket: s.Bucket, Key: s.Key, Account: s.Target.Account,
		Region: s.Region, Reason: reason,
	})
}

func stateFindings(s *tfstate.UnitState, p config.AuditPolicy) []Finding {
	var out []Finding
	switch s.Status {
	case tfstate.StatusAbsent:
		return []Finding{
			stateFinding(CheckAbsent, s, "s3://"+s.Bucket+"/"+s.Key+" doesn't exist: never applied, or a wrong key"),
		}
	case tfstate.StatusOK:
		if s.Instances == 0 {
			out = append(out, stateFinding(CheckEmpty, s, "the state manages no resources"))
		}
	case tfstate.StatusEncrypted, tfstate.StatusUnsupported:
	case tfstate.StatusError, tfstate.StatusSkipped:
		return nil
	}
	if s.TerraformVersion != "" && olderThan(s.TerraformVersion, p.MinTerraformVersion) {
		out = append(out, stateFinding(CheckTFVersion, s, fmt.Sprintf(
			"written by %s, older than the policy's %s", s.TerraformVersion, p.MinTerraformVersion)))
	}
	for _, pv := range s.Providers {
		want := p.MinProviderVersions[pv.Source]
		if want != "" && pv.Version != "" && olderThan(pv.Version, want) {
			out = append(out, stateFinding(CheckProvider, s, fmt.Sprintf(
				"%s %s in .terraform.lock.hcl is older than the policy's %s", pv.Source, pv.Version, want)))
		}
	}
	if limit := p.MaxStateMB * mib; limit > 0 && float64(s.Size) > limit {
		out = append(out, stateFinding(CheckSize, s, fmt.Sprintf(
			"state is %s, over the policy's %s MiB",
			sizeLabel(s.Size),
			strconv.FormatFloat(p.MaxStateMB, 'f', -1, 64),
		)))
	}
	if names := sensitiveNames(s.State); len(names) > 0 {
		out = append(out, stateFinding(CheckSecret, s, "sensitive-looking attributes stored in plain state: "+
			strings.Join(names, "; ")))
	}
	return out
}

// sensitiveNames lists address (attr, attr) for every resource instance with sensitive
// attribute names. Values are never read.
func sensitiveNames(st *tfstate.State) []string {
	if st == nil {
		return nil
	}
	var out []string
	for _, r := range st.Resources {
		for _, inst := range r.Instances {
			if len(inst.Sensitive) > 0 {
				out = append(out, r.Address(&inst)+" ("+strings.Join(inst.Sensitive, ", ")+")")
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// lineageFindings flags lineages that appear under more than one key: one state is a copy.
func lineageFindings(states []tfstate.UnitState) []Finding {
	byLineage := map[string][]*tfstate.UnitState{}
	for i := range states {
		if s := &states[i]; s.Lineage != "" {
			byLineage[s.Lineage] = append(byLineage[s.Lineage], s)
		}
	}
	var out []Finding
	for _, lineage := range slices.Sorted(maps.Keys(byLineage)) {
		group := byLineage[lineage]
		if len(group) < 2 { //nolint:mnd // one state per lineage is the normal case
			continue
		}
		labels := make([]string, len(group))
		for i, s := range group {
			labels[i] = stateLabel(s) + " (s3://" + s.Bucket + "/" + s.Key + ")"
		}
		for _, s := range group {
			out = append(
				out,
				stateFinding(CheckLineage, s, "lineage "+lineage+" is shared by "+strings.Join(labels, " and ")),
			)
		}
	}
	return out
}

func doubleFindings(dms []tfstate.DoubleManaged) []Finding {
	var out []Finding
	for _, dm := range dms {
		refs := make([]string, len(dm.Refs))
		for i, r := range dm.Refs {
			refs[i] = r.Unit
			if r.Workspace != tfstate.DefaultWorkspace {
				refs[i] += ":" + r.Workspace
			}
			refs[i] += " (" + r.Address + ")"
		}
		out = append(out, newFinding(CheckDouble, Finding{
			Target: dm.Refs[0].Unit,
			Unit:   dm.Refs[0].Unit,
			Reason: fmt.Sprintf(
				"%s %s is managed by %s (matched on %s)",
				dm.Type,
				dm.Key,
				strings.Join(refs, " and "),
				dm.Kind,
			),
		}))
	}
	return out
}

func sizeLabel(n int64) string {
	const kib = 1 << 10
	if n < mib {
		return fmt.Sprintf("%.1f KiB", float64(n)/kib)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/mib)
}

// olderThan reports whether version v is older than minimum, comparing dotted numbers. A
// pre-release such as 1.10.0-beta1 counts as its release.
func olderThan(v, minimum string) bool {
	a, b := versionParts(v), versionParts(minimum)
	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "-")
	var out []int
	for p := range strings.SplitSeq(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}
