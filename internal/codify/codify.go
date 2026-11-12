package codify

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Import keys.
const (
	ByIdentity = "identity"
	ByID       = "id"
)

// Options configures [Generate].
type Options struct {
	Map        *typemap.Map
	Target     Target
	ImportByID bool // write id even when identity is possible
	Types      scan.TypeMatcher
	Select     *ownership.Selector // nil selects every finding
	Query      bool                // also write a .tfquery.hcl file
	Existing   *Existing           // nil when the directory has no .tf files
	From       string              // the scan report's file name, for the header
	Date       time.Time
}

// Import is one import block.
type Import struct {
	Address  string            `json:"address"`
	Type     string            `json:"type"`
	Name     string            `json:"name"`
	Account  string            `json:"account"`
	Region   string            `json:"region,omitempty"`
	ARN      string            `json:"arn,omitempty"`
	CloudID  string            `json:"cloud_id,omitempty"`
	By       string            `json:"by"` // identity or id
	Identity map[string]string `json:"identity,omitempty"`
	ID       string            `json:"id,omitempty"`

	base string
}

// key returns the ARN, else the cloud ID.
func (i *Import) key() string { return cmp.Or(i.ARN, i.CloudID) }

// List is one list block of the query file.
type List struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Region string `json:"region,omitempty"` // empty for a global type
}

// Skip is a selected finding that got no import block.
type Skip struct {
	Key    string `json:"key"`
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// File is a file to write into the target directory.
type File struct {
	Name    string
	Content []byte
}

// Result is what [Generate] produced.
type Result struct {
	Target   Target   `json:"target"`
	Selected int      `json:"selected"`
	Imports  []Import `json:"imports"`
	Lists    []List   `json:"lists"`
	Skipped  []Skip   `json:"skipped"`
	Notes    []string `json:"notes"`
	Files    []File   `json:"-"`
}

// FileNames returns the names of the files to write.
func (r *Result) FileNames() []string {
	out := make([]string, len(r.Files))
	for i, f := range r.Files {
		out[i] = f.Name
	}
	return out
}

// Generate turns the unmanaged findings that o selects into import blocks, and with o.Query a
// query file. The output depends only on findings and o, in any order of findings.
func Generate(findings []scan.Finding, o Options) (*Result, error) {
	res := &Result{Target: o.Target, Imports: []Import{}, Lists: []List{}, Skipped: []Skip{}, Notes: []string{}}
	existing := o.Existing
	if existing == nil {
		existing = &Existing{names: names{}, keys: map[string]string{}}
	}
	identity := !o.ImportByID && o.Target.IdentityEngine() && o.Target.IdentityProvider()
	res.Notes = append(res.Notes, targetNotes(&o, identity)...)
	seen := map[string]bool{}
	var imports []Import
	for i := range findings {
		f := &findings[i]
		ok, err := selected(f, &o)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		res.Selected++
		imp, skip := importFor(f, &o, identity)
		if skip != nil {
			res.Skipped = append(res.Skipped, *skip)
			continue
		}
		k := importKey(&imp)
		if file, found := existing.keys[k]; found {
			res.Skipped = append(
				res.Skipped,
				Skip{Key: imp.key(), Type: imp.Type, Reason: "already imported in " + file},
			)
			continue
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		imports = append(imports, imp)
	}
	slices.SortFunc(imports, func(a, b Import) int {
		return cmp.Or(
			strings.Compare(a.Type, b.Type),
			strings.Compare(a.base, b.base),
			strings.Compare(a.key(), b.key()),
		)
	})
	taken := names{}
	for t, ns := range existing.names {
		taken[t] = maps.Clone(ns)
	}
	for i := range imports {
		imp := &imports[i]
		imp.Name = taken.next(imp.Type, imp.base)
		imp.Address = imp.Type + "." + imp.Name
	}
	res.Imports = imports
	slices.SortFunc(res.Skipped, func(a, b Skip) int {
		return cmp.Or(strings.Compare(a.Type, b.Type), strings.Compare(a.Key, b.Key))
	})
	names := OutputNames(o.Date)
	if len(imports) > 0 {
		res.Files = append(res.Files, File{Name: names[0], Content: importsFile(imports, &o)})
	}
	if o.Query && len(imports) > 0 {
		lists, notes := listsFor(imports, &o)
		res.Notes = append(res.Notes, notes...)
		if len(lists) > 0 {
			res.Lists = lists
			res.Files = append(res.Files, File{Name: names[1], Content: queryFile(lists, &o)})
		}
	}
	return res, nil
}

// OutputNames returns the names of the imports file and the query file for date, in UTC.
func OutputNames(date time.Time) []string {
	d := date.UTC().Format("20060102")
	return []string{"imports_tfcensus_" + d + ".tf", "query_tfcensus_" + d + ".tfquery.hcl"}
}

func targetNotes(o *Options, identity bool) []string {
	var notes []string
	switch {
	case o.ImportByID:
		notes = append(notes, "--import-by id: writing id imports")
	case !o.Target.IdentityEngine():
		notes = append(notes, fmt.Sprintf("target %s has no import identity (terraform and opentofu 1.12+): "+
			"writing id imports", o.Target))
	case !o.Target.IdentityProvider():
		notes = append(notes, fmt.Sprintf("aws provider %s in the lock file has no resource identity (6.0.0+): "+
			"writing id imports", o.Target.Provider))
	}
	if identity && o.Target.Provider != nil && len(o.Map.Sources) > 0 {
		if gen := o.Map.Sources[0]; providerOlder(o.Target.Provider.String(), gen.Version) {
			notes = append(notes, fmt.Sprintf("the identity data is from aws provider %s and the lock file has %s: "+
				"a type that gained identity later fails with \"unknown identity type\"; use --import-by id for it",
				gen.Version, o.Target.Provider))
		}
	}
	return notes
}

func providerOlder(locked, generated string) bool {
	a, errA := version.NewVersion(locked)
	b, errB := version.NewVersion(generated)
	return errA == nil && errB == nil && a.Core().LessThan(b.Core())
}

// selected reports whether codify takes f: an unmanaged finding that --types and --select pick.
func selected(f *scan.Finding, o *Options) (bool, error) {
	if f.Category != scan.CategoryUnmanaged || !o.Types.Match(f.CFNType, f.SourceType, f.TFTypes) {
		return false, nil
	}
	if o.Select == nil {
		return true, nil
	}
	r := cloudResource(f)
	ok, err := o.Select.Match(ownership.FromCloud(&r, o.Map, nil))
	if err != nil {
		return false, fmt.Errorf("--select on %s: %w", f.Key(), err)
	}
	return ok, nil
}

func cloudResource(f *scan.Finding) inventory.CloudResource {
	return inventory.CloudResource{
		Account: f.Account, Region: f.Region, CFNType: f.CFNType, SourceType: f.SourceType, TFTypes: f.TFTypes,
		ARN: f.ARN, ID: f.ID, Name: f.Name, Tags: f.Tags, Attrs: map[string]string{}, Created: f.Created,
		Sources: f.Sources,
	}
}

// candidates returns the TF types to try for f: those --types names by TF type, else all of f's.
func candidates(f *scan.Finding, o *Options) []string {
	var named []string
	for _, tf := range f.TFTypes {
		if len(o.Types) > 0 && o.Types.Match("", "", []string{tf}) {
			named = append(named, tf)
		}
	}
	if len(named) > 0 {
		return named
	}
	return f.TFTypes
}

// importFor builds the import block of f from the first candidate type that gives a key.
func importFor(f *scan.Finding, o *Options, identity bool) (Import, *Skip) {
	r := cloudResource(f)
	var why []string
	tfTypes := candidates(f, o)
	if len(tfTypes) == 0 {
		return Import{}, &Skip{Key: f.Key(), Type: f.Type(), Reason: "no TF type maps to " + f.Type()}
	}
	for _, tf := range tfTypes {
		t, ok := o.Map.Type(tf)
		if !ok {
			why = append(why, tf+": not in the typemap")
			continue
		}
		imp := Import{
			Type: tf, Account: f.Account, Region: f.Region, ARN: f.ARN, CloudID: f.ID,
			base: Slug(cmp.Or(f.Tags["Name"], f.Name, lastSegment(cmp.Or(f.ID, f.ARN)))),
		}
		if identity {
			if id, found := match.ImportIdentity(t, &r); found {
				imp.By, imp.Identity = ByIdentity, id
				return imp, nil
			}
		}
		if id, found := match.ImportID(t, &r); found {
			imp.By, imp.ID = ByID, id
			return imp, nil
		}
		switch {
		case identity && len(t.IdentityAttrs) > 0:
			why = append(why, tf+": an identity attribute has no value in the scan and the type has no id template")
		case len(t.IdentityAttrs) > 0:
			why = append(why, tf+": no id template, and the target can't import by identity")
		default:
			why = append(why, tf+": no identity and no id template")
		}
	}
	return Import{}, &Skip{Key: f.Key(), Type: tfTypes[0], Reason: strings.Join(why, "; ")}
}

func importKey(imp *Import) string {
	if imp.By == ByIdentity {
		return identityKey(imp.Type, imp.Identity)
	}
	return idKey(imp.Type, imp.ID)
}

func comment(text string) hclwrite.Tokens {
	text = strings.NewReplacer("\n", " ", "\r", " ").Replace(text)
	return hclwrite.Tokens{{Type: hclsyntax.TokenComment, Bytes: []byte("# " + text + "\n")}}
}

func importsFile(imports []Import, o *Options) []byte {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(comment("Generated by tfcensus codify from " + cmp.Or(o.From, "a scan report") +
		". A starting point: review every block before you plan."))
	body.AppendUnstructuredTokens(comment("Target: " + o.Target.String() + "."))
	for i := range imports {
		imp := &imports[i]
		body.AppendNewline()
		b := body.AppendNewBlock("import", nil).Body()
		b.AppendUnstructuredTokens(comment(imp.key()))
		b.SetAttributeTraversal("to", hcl.Traversal{hcl.TraverseRoot{Name: imp.Type}, hcl.TraverseAttr{Name: imp.Name}})
		if imp.By == ByIdentity {
			vals := make(map[string]cty.Value, len(imp.Identity))
			for k, v := range imp.Identity {
				vals[k] = cty.StringVal(v)
			}
			b.SetAttributeValue("identity", cty.ObjectVal(vals))
		} else {
			b.SetAttributeValue("id", cty.StringVal(imp.ID))
		}
	}
	return hclwrite.Format(f.Bytes())
}

// listsFor returns one list block per type and region, for the imported types that have a
// provider list resource without required list config.
func listsFor(imports []Import, o *Options) ([]List, []string) {
	if !o.Target.Query() {
		why := fmt.Sprintf("target %s has no terraform query (terraform 1.14+)", o.Target)
		if o.Target.Engine == EngineOpenTofu {
			why = "OpenTofu has no query support yet (opentofu#3787)"
		}
		return nil, []string{"--query: " + why + ": no query file written"}
	}
	seen := map[List]bool{}
	var lists []List
	skipped := map[string]string{}
	for i := range imports {
		t, ok := o.Map.Type(imports[i].Type)
		switch {
		case !ok || !t.List:
			skipped[imports[i].Type] = "has no list resource"
			continue
		case len(t.ListRequired) > 0:
			skipped[imports[i].Type] = "needs list config " + strings.Join(t.ListRequired, ", ")
			continue
		}
		l := List{Type: t.TF}
		if !t.Global {
			l.Region = imports[i].Region
		}
		l.Name = "tfcensus_" + Slug(cmp.Or(l.Region, "global"))
		if !seen[l] {
			seen[l] = true
			lists = append(lists, l)
		}
	}
	slices.SortFunc(lists, func(a, b List) int {
		return cmp.Or(strings.Compare(a.Type, b.Type), strings.Compare(a.Name, b.Name))
	})
	notes := make([]string, 0, len(skipped))
	for _, tf := range slices.Sorted(maps.Keys(skipped)) {
		notes = append(notes, "--query: "+tf+" "+skipped[tf]+": no list block")
	}
	return lists, notes
}

func queryFile(lists []List, o *Options) []byte {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(comment("Generated by tfcensus codify from " + cmp.Or(o.From, "a scan report") +
		". Each block lists every resource of its type, managed or not."))
	for _, l := range lists {
		body.AppendNewline()
		b := body.AppendNewBlock("list", []string{l.Type, l.Name}).Body()
		b.SetAttributeTraversal("provider", hcl.Traversal{hcl.TraverseRoot{Name: "aws"}})
		b.SetAttributeValue("include_resource", cty.True)
		if l.Region != "" {
			b.AppendNewline()
			b.AppendNewBlock("config", nil).Body().SetAttributeValue("region", cty.StringVal(l.Region))
		}
	}
	return hclwrite.Format(f.Bytes())
}
