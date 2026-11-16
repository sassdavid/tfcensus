package inventory

import (
	"cmp"
	"slices"
	"strings"
)

// Reasons of a [Note].
const (
	// NoteKubernetes counts Kubernetes objects that Resource Explorer indexes for EKS clusters.
	// They aren't AWS infrastructure, and no Terraform AWS resource manages them.
	NoteKubernetes = "skipped: Kubernetes object"
	// NoteStale counts records only Resource Explorer listed, of a type AWS Config recorded in
	// the same account and region. Resource Explorer is eventually consistent, and Config is
	// authoritative for the types it records.
	NoteStale = "dropped: only Resource Explorer listed it, and AWS Config records the type"
)

// Note counts records a run left out of the inventory, and why.
type Note struct {
	Account string `json:"account"`
	Region  string `json:"region,omitempty"`
	Layer   string `json:"layer"`
	Reason  string `json:"reason"`
	Count   int    `json:"count"`
}

// kubernetesTypes are the Resource Explorer types of Kubernetes objects.
func kubernetesTypes() []string {
	return []string{
		"eks:daemonset", "eks:deployment", "eks:endpointslice", "eks:ingress", "eks:namespace",
		"eks:persistentvolume", "eks:replicaset", "eks:service", "eks:statefulset",
	}
}

// skipKubernetes removes the Kubernetes objects from every task's records and counts them.
func skipKubernetes(tasks []*task) []Note {
	var notes []Note
	for _, t := range tasks {
		n := 0
		t.found = slices.DeleteFunc(t.found, func(r CloudResource) bool {
			skip := r.CFNType == "" && slices.Contains(kubernetesTypes(), r.SourceType)
			if skip {
				n++
			}
			return skip
		})
		if n > 0 {
			notes = addNote(
				notes,
				Note{Account: t.run.Account, Region: t.region, Layer: t.run.Layer, Reason: NoteKubernetes, Count: n},
			)
		}
	}
	return notes
}

// dropStale removes the merged records that only Resource Explorer listed when a config task
// that records every supported type listed their type in the same account and region. A global
// record counts for any region whose recorder records global types.
func dropStale(resources []CloudResource, tasks []*task, notes []Note) ([]CloudResource, []Note) {
	out := resources[:0:0]
	for i := range resources {
		r := &resources[i]
		if slices.Equal(r.Sources, []string{LayerRE}) && r.CFNType != "" && configListed(tasks, r) {
			notes = addNote(
				notes,
				Note{Account: r.Account, Region: r.Region, Layer: LayerRE, Reason: NoteStale, Count: 1},
			)
			continue
		}
		out = append(out, *r)
	}
	return out, notes
}

func configListed(tasks []*task, r *CloudResource) bool {
	return slices.ContainsFunc(tasks, func(t *task) bool {
		if t.layer.Name() != LayerConfig || t.run.Account != r.Account ||
			(t.run.Status != RunOK && t.run.Status != RunPartial) {
			return false
		}
		if r.Region == "" {
			if !t.caps.ConfigGlobal {
				return false
			}
		} else if t.region != r.Region || !t.caps.ConfigAll {
			return false
		}
		return slices.Contains(t.layer.Types(), r.CFNType) && wanted(t.types, r.CFNType)
	})
}

// addNote adds n to the note with the same account, region, layer and reason.
func addNote(notes []Note, n Note) []Note {
	for i := range notes {
		o := &notes[i]
		if o.Account == n.Account && o.Region == n.Region && o.Layer == n.Layer && o.Reason == n.Reason {
			o.Count += n.Count
			return notes
		}
	}
	return append(notes, n)
}

func sortNotes(notes []Note) []Note {
	slices.SortFunc(notes, func(a, b Note) int {
		return cmp.Or(strings.Compare(a.Account, b.Account), strings.Compare(a.Region, b.Region),
			strings.Compare(a.Layer, b.Layer), strings.Compare(a.Reason, b.Reason))
	})
	return notes
}
