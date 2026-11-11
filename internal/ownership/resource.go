package ownership

import (
	"cmp"
	"maps"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Index finds inventory records by ARN, or by account, region, type and ID.
type Index struct {
	byARN map[string]*inventory.CloudResource
	byID  map[string]*inventory.CloudResource
}

// NewIndex indexes resources, which must outlive the Index.
func NewIndex(resources []inventory.CloudResource) *Index {
	ix := &Index{byARN: map[string]*inventory.CloudResource{}, byID: map[string]*inventory.CloudResource{}}
	for i := range resources {
		r := &resources[i]
		if r.ARN != "" {
			ix.byARN[r.ARN] = r
		}
		if r.ID != "" {
			ix.byID[idKey(r.Account, r.Region, r.CFNType, r.ID)] = r
		}
	}
	return ix
}

func idKey(account, region, cfn, id string) string {
	return account + "|" + region + "|" + cfn + "|" + id
}

// find returns the record ref points at, looked up in r's account and region, then account-wide.
func (ix *Index) find(r *inventory.CloudResource, ref inventory.Ref) *inventory.CloudResource {
	if ix == nil {
		return nil
	}
	if ref.ARN != "" {
		if f, ok := ix.byARN[typemap.NormalizeARN(ref.ARN)]; ok {
			return f
		}
	}
	if f, ok := ix.byID[idKey(r.Account, r.Region, ref.CFNType, ref.ID)]; ok {
		return f
	}
	return ix.byID[idKey(r.Account, "", ref.CFNType, ref.ID)]
}

// FromCloud builds the rule input for r. A missing name comes from the ARN's name placeholder,
// such as a log group's name. Related resources carry the inventory's tags and attributes.
func FromCloud(r *inventory.CloudResource, m *typemap.Map, ix *Index) *Resource {
	out := &Resource{
		Account: r.Account, Region: r.Region, Type: r.CFNType, SourceType: r.SourceType, TFTypes: r.TFTypes,
		ARN: r.ARN, ID: r.ID, Name: r.Name, Tags: cloneOrEmpty(r.Tags), Attrs: cloneOrEmpty(r.Attrs),
		Sources: r.Sources, Created: r.Created, Parents: []Related{}, Relationships: []Related{},
	}
	if len(r.Sources) > 0 {
		out.Layer = r.Sources[0]
	}
	if out.Name == "" && r.ARN != "" && m != nil {
		if _, fields := m.Classify(r.ARN); fields != nil {
			out.Name = cmp.Or(fields["name"], fields["function_name"])
		}
	}
	for _, p := range r.Parents {
		out.Parents = append(out.Parents, related(r, p, ix))
	}
	for _, rel := range r.Relationships {
		out.Relationships = append(out.Relationships, related(r, rel, ix))
	}
	return out
}

func related(r *inventory.CloudResource, ref inventory.Ref, ix *Index) Related {
	rel := Related{
		Type: ref.CFNType, ID: ref.ID, ARN: ref.ARN, Relationship: ref.Relationship,
		Tags: map[string]string{}, Attrs: map[string]string{},
	}
	if f := ix.find(r, ref); f != nil {
		rel.Found, rel.Name, rel.ARN = true, f.Name, cmp.Or(rel.ARN, f.ARN)
		rel.Tags, rel.Attrs = cloneOrEmpty(f.Tags), cloneOrEmpty(f.Attrs)
	}
	return rel
}

// cloneOrEmpty returns a copy of m, or an empty map, so rules can test keys with in.
func cloneOrEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return maps.Clone(m)
}
