package typemap

import (
	"bytes"
	"cmp"
	"embed"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

//go:embed data/generated.yaml data/curated.yaml
var data embed.FS

// NativePrefix starts the CloudFormation-style type names tfcensus gives to resources that
// CloudFormation has no type for, such as IAM policy attachments.
const NativePrefix = "Native::"

// Identity attributes that the provider adds to every regional or global identity.
const (
	IdentityAccountID = "account_id"
	IdentityRegion    = "region"
)

// Generated is the content of data/generated.yaml, written by `mise run typemap:gen`.
type Generated struct {
	Sources   []Source      `yaml:"sources"`
	Resources []GenResource `yaml:"resources"`
	CFNTypes  []string      `yaml:"cfn_types"`
}

// Source is a provider checkout the generated data was read from.
type Source struct {
	Repo    string `yaml:"repo"`
	Version string `yaml:"version"`
	Commit  string `yaml:"commit"`
}

// GenResource is one aws provider resource type as the provider source declares it.
type GenResource struct {
	TF            string            `yaml:"tf"`
	Service       string            `yaml:"service"`
	Global        bool              `yaml:"global,omitempty"`
	Identity      string            `yaml:"identity,omitempty"`       // identity kind, such as regional-single-parameter
	IdentityAttrs []string          `yaml:"identity_attrs,omitempty"` // in schema order
	IdentityFrom  map[string]string `yaml:"identity_from,omitempty"`  // identity attribute to resource attribute, when they differ
	// IdentityOptional lists the type's own identity attributes that may be empty.
	IdentityOptional []string `yaml:"identity_optional,omitempty"`
	// List reports whether the provider has a list resource for the type, which terraform query
	// uses.
	List         bool     `yaml:"list,omitempty"`
	ListRequired []string `yaml:"list_required,omitempty"` // list config attributes a list block must set
	RE           []string `yaml:"re,omitempty"`            // tagging and Resource Explorer types
	CFN          string   `yaml:"cfn,omitempty"`           // a candidate from awscc's type names, unconfirmed
}

// Curated is one hand-written entry of data/curated.yaml.
type Curated struct {
	TF        string            `yaml:"tf"`
	CFN       Strings           `yaml:"cfn"`
	Config    string            `yaml:"config,omitempty"` // the AWS Config type, when Config records it
	RE        Strings           `yaml:"re,omitempty"`     // replaces the generated types
	ARN       string            `yaml:"arn,omitempty"`
	ID        []Template        `yaml:"id,omitempty"`   // the TF id, tried in order
	Live      map[string]string `yaml:"live,omitempty"` // fields rendered from other fields
	Native    string            `yaml:"native,omitempty"`
	RuleTuple bool              `yaml:"rule_tuple,omitempty"` // match security group rules by their tuple
	Derived   *Derived          `yaml:"derived,omitempty"`
	CCParent  *CCParent         `yaml:"cc_parent,omitempty"`
}

// CCParent says how Cloud Control lists a type that needs a parent: one ListResources call per
// parent, with the parent's ARN in Property of the resource model.
type CCParent struct {
	Type     string `yaml:"type"`     // the parent's CloudFormation type
	Property string `yaml:"property"` // the resource model property, such as LoadBalancerArn
}

// Derived marks a type whose resources AWS creates as children of a parent resource.
type Derived struct {
	Parent string              `yaml:"parent"`         // the parent's CloudFormation type
	When   map[string][]string `yaml:"when,omitempty"` // field values that make a resource derived
}

// Strings is a YAML scalar or sequence of strings.
type Strings []string

// UnmarshalYAML accepts a single string or a list of strings.
func (s *Strings) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = Strings{n.Value}
		return nil
	}
	var list []string
	if err := n.Decode(&list); err != nil {
		return fmt.Errorf("decode string list: %w", err)
	}
	*s = list
	return nil
}

// Type is everything tfcensus knows about one TF resource type.
type Type struct {
	GenResource

	Curated  bool
	CFN      []string
	Config   string
	RE       []string
	ARN      *Pattern
	ID       []Template
	Live     map[string]Template
	Native   string
	Tuple    bool
	Derived  *Derived
	CCParent *CCParent
}

// Map is the loaded typemap.
type Map struct {
	Sources []Source

	types    map[string]*Type
	byCFN    map[string][]*Type
	byConfig map[string]string
	byRE     map[string][]string
	cfn      map[string]bool
	withARN  []*Type
}

// Load parses the embedded typemap.
func Load() (*Map, error) {
	gen, err := data.ReadFile("data/generated.yaml")
	if err != nil {
		return nil, fmt.Errorf("read typemap: %w", err)
	}
	cur, err := data.ReadFile("data/curated.yaml")
	if err != nil {
		return nil, fmt.Errorf("read typemap: %w", err)
	}
	return Parse(gen, cur)
}

// ErrInvalid is returned for a typemap entry that doesn't fit the rest.
var ErrInvalid = errors.New("invalid typemap")

func decodeStrict(b []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// Parse builds a Map from the generated and curated YAML.
func Parse(generated, curated []byte) (*Map, error) {
	var gen Generated
	if err := decodeStrict(generated, &gen); err != nil {
		return nil, fmt.Errorf("generated typemap: %w", err)
	}
	var cur []Curated
	if err := decodeStrict(curated, &cur); err != nil {
		return nil, fmt.Errorf("curated typemap: %w", err)
	}
	m := &Map{
		Sources: gen.Sources, types: map[string]*Type{}, byCFN: map[string][]*Type{},
		byConfig: map[string]string{}, byRE: map[string][]string{}, cfn: map[string]bool{},
	}
	for _, c := range gen.CFNTypes {
		m.cfn[c] = true
	}
	for _, r := range gen.Resources {
		m.types[r.TF] = &Type{GenResource: r, RE: r.RE}
	}
	var errs []error
	for i := range cur {
		if err := m.addCurated(&cur[i]); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	m.index()
	return m, nil
}

func (m *Map) addCurated(c *Curated) error {
	t, ok := m.types[c.TF]
	if !ok {
		return fmt.Errorf("%w: %s is not an aws provider resource", ErrInvalid, c.TF)
	}
	if t.Curated {
		return fmt.Errorf("%w: %s is curated twice", ErrInvalid, c.TF)
	}
	if len(c.CFN) == 0 {
		return fmt.Errorf("%w: %s has no cfn type", ErrInvalid, c.TF)
	}
	for _, cfn := range c.CFN {
		if !m.cfn[cfn] && !strings.HasPrefix(cfn, NativePrefix) {
			return fmt.Errorf("%w: %s: %s is not a CloudFormation type", ErrInvalid, c.TF, cfn)
		}
	}
	if len(c.ID) == 0 && !c.RuleTuple && c.ARN == "" && t.Identity == "" {
		return fmt.Errorf("%w: %s has no identity, arn, id or rule tuple to match on", ErrInvalid, c.TF)
	}
	if c.CCParent != nil && (c.CCParent.Property == "" || !m.cfn[c.CCParent.Type]) {
		return fmt.Errorf("%w: %s: cc_parent needs a CloudFormation type and a property", ErrInvalid, c.TF)
	}
	t.Curated, t.CFN, t.Config, t.ID, t.Native, t.Tuple, t.Derived, t.CCParent =
		true, c.CFN, c.Config, c.ID, c.Native, c.RuleTuple, c.Derived, c.CCParent
	if len(c.RE) > 0 {
		t.RE = c.RE
	}
	if c.ARN != "" {
		p, err := CompilePattern(c.ARN)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrInvalid, c.TF, err)
		}
		t.ARN = p
	}
	if len(c.Live) > 0 {
		t.Live = map[string]Template{}
		for k, v := range c.Live {
			t.Live[k] = Template(v)
		}
	}
	return nil
}

func (m *Map) index() {
	tfs := slices.Sorted(maps.Keys(m.types))
	for _, tf := range tfs {
		if t := m.types[tf]; t.Curated {
			m.indexCurated(t)
		}
	}
	// Guesses come after every curated type, so a guess never shadows a confirmed mapping.
	for _, tf := range tfs {
		t := m.types[tf]
		if t.Curated || t.GenResource.CFN == "" {
			continue
		}
		for _, re := range t.RE {
			m.byRE[re] = appendUnique(m.byRE[re], t.GenResource.CFN)
		}
	}
}

func (m *Map) indexCurated(t *Type) {
	for _, c := range t.CFN {
		m.byCFN[c] = append(m.byCFN[c], t)
	}
	if t.ARN != nil {
		m.withARN = append(m.withARN, t)
	}
	if len(t.CFN) != 1 {
		return
	}
	if t.Config != "" {
		m.byConfig[t.Config] = t.CFN[0]
	}
	for _, re := range t.RE {
		m.byRE[re] = appendUnique(m.byRE[re], t.CFN[0])
	}
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

// Type returns the entry for a TF resource type.
func (m *Map) Type(tf string) (*Type, bool) {
	t, ok := m.types[tf]
	return t, ok
}

// Curated returns the curated types, sorted by TF type.
func (m *Map) Curated() []*Type {
	var out []*Type
	for _, tf := range slices.Sorted(maps.Keys(m.types)) {
		if t := m.types[tf]; t.Curated {
			out = append(out, t)
		}
	}
	return out
}

// TFTypes returns every TF resource type in the typemap, sorted.
func (m *Map) TFTypes() []string { return slices.Sorted(maps.Keys(m.types)) }

// CFNTypes returns every CloudFormation type and mapped native type in the typemap, sorted.
func (m *Map) CFNTypes() []string {
	set := maps.Clone(m.cfn)
	for cfn := range m.byCFN {
		set[cfn] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// Generated returns the number of TF resource types in the generated data.
func (m *Map) Generated() int { return len(m.types) }

// ForCFN returns the curated TF types for a CloudFormation type, sorted by TF type.
func (m *Map) ForCFN(cfn string) []*Type { return m.byCFN[cfn] }

// Mapped reports whether a CloudFormation type has a curated TF type.
func (m *Map) Mapped(cfn string) bool { return len(m.byCFN[cfn]) > 0 }

// IsCFN reports whether cfn is a CloudFormation type or a tfcensus native type.
func (m *Map) IsCFN(cfn string) bool { return m.cfn[cfn] || strings.HasPrefix(cfn, NativePrefix) }

// CCParent returns how Cloud Control lists a CloudFormation type that needs a parent, or nil.
func (m *Map) CCParent(cfn string) *CCParent {
	for _, t := range m.byCFN[cfn] {
		if t.CCParent != nil {
			return t.CCParent
		}
	}
	return nil
}

// FromConfig returns the CloudFormation type for an AWS Config type. Most are the same.
func (m *Map) FromConfig(configType string) string {
	return cmp.Or(m.byConfig[configType], configType)
}

// ConfigTypes returns the AWS Config types for the CloudFormation types, or for every curated
// type that Config records when cfnTypes is empty. The result is sorted.
func (m *Map) ConfigTypes(cfnTypes []string) []string {
	var out []string
	for _, t := range m.Curated() {
		if t.Config == "" || len(t.CFN) != 1 {
			continue
		}
		if len(cfnTypes) == 0 || slices.Contains(cfnTypes, t.CFN[0]) {
			out = appendUnique(out, t.Config)
		}
	}
	slices.Sort(out)
	return out
}

// FromRE returns the CloudFormation type for a Resource Explorer type, or "" when it is unknown
// or ambiguous.
func (m *Map) FromRE(reType string) string {
	if c := m.byRE[reType]; len(c) > 0 {
		return c[0]
	}
	return ""
}

// HasRE reports whether Resource Explorer indexes the CloudFormation type.
func (m *Map) HasRE(cfn string) bool {
	return slices.ContainsFunc(m.byCFN[cfn], func(t *Type) bool { return len(t.RE) > 0 })
}

// Classify returns the CloudFormation type of an ARN and the values its pattern captured, from
// the first curated type in TF order whose pattern matches. It returns "" for an unknown ARN.
func (m *Map) Classify(arn string) (string, map[string]string) {
	arn = NormalizeARN(arn)
	for _, t := range m.withARN {
		if f := t.ARN.Match(arn); f != nil {
			return t.CFN[0], f
		}
	}
	return "", nil
}
