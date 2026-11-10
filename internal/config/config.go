package config

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is the config schema_version this binary reads.
const SchemaVersion = 1

// Config defines every configuration key. The JSON tags are the key names, and
// schemas/config-v1.json is generated from this type.
//
// rules, audit.suppress, drift.noise and notify are free-form until the milestone that owns
// them defines their shape.
type Config struct {
	SchemaVersion int       `json:"schema_version" jsonschema:"enum=1" jsonschema_description:"Config file format version."`
	Binaries      Binaries  `json:"binaries"`
	Discovery     Discovery `json:"discovery"`
	Accounts      Accounts  `json:"accounts"`
	Regions       []string  `json:"regions"                            jsonschema_description:"Regions to scan. Empty means the regions found in the units' backends and providers."`
	Scan          Scan      `json:"scan"`
	Rules         []Object  `json:"rules"                              jsonschema_description:"Ownership rules."`
	Audit         Audit     `json:"audit"`
	Drift         Drift     `json:"drift"`
	Notify        Object    `json:"notify"                             jsonschema_description:"Notifiers."`
	Cache         Cache     `json:"cache"`
}

// Object is a free-form mapping.
type Object = map[string]any

// Binaries names the external tools tfcensus runs.
type Binaries struct {
	Terragrunt string `json:"terragrunt" jsonschema:"minLength=1" jsonschema_description:"terragrunt binary, a name on PATH or a path."`
	Terraform  string `json:"terraform"  jsonschema:"minLength=1" jsonschema_description:"terraform or tofu binary, passed to terragrunt as --tf-path."`
}

// Discovery configures unit discovery.
type Discovery struct {
	Roots          []string `json:"roots"           jsonschema_description:"Directories to discover units in."`
	GenerateStacks bool     `json:"generate_stacks" jsonschema_description:"Run terragrunt stack generate before discovery."`
	Render         string   `json:"render"          jsonschema_description:"Use terragrunt render: auto, always or never (fast path only)." jsonschema:"enum=auto,enum=always,enum=never"`
}

// Accounts configures where AWS accounts come from.
type Accounts struct {
	Profiles   []string              `json:"profiles"   jsonschema_description:"AWS profile globs from ~/.aws/config."`
	Org        Org                   `json:"org"`
	Map        map[string]AccountRef `json:"map"        jsonschema_description:"Account ID to profile and display name, for when profile names don't match accounts."`
	Aggregator Aggregator            `json:"aggregator"`
}

// Org configures AWS Organizations account listing.
type Org struct {
	Enabled           bool   `json:"enabled"`
	ManagementProfile string `json:"management_profile"`
	Role              string `json:"role"               jsonschema:"minLength=1"`
}

// AccountRef maps an account ID to a profile and a display name.
type AccountRef struct {
	Profile string `json:"profile"`
	Name    string `json:"name"`
}

// Aggregator locates the AWS Config aggregator.
type Aggregator struct {
	Profile string `json:"profile"`
	Name    string `json:"name"`
	Region  string `json:"region"`
}

// Scan configures the unmanaged-resource scan.
type Scan struct {
	Layers            string   `json:"layers"             jsonschema_description:"Inventory layers: auto, or a comma-separated list of re, config, cc and native."`
	CloudControlTypes []string `json:"cloudcontrol_types" jsonschema_description:"CloudFormation types listed through Cloud Control."`
	Packs             Packs    `json:"packs"`
	NeverHide         []string `json:"never_hide"`
}

// Packs turns the built-in ownership rule packs on or off.
type Packs struct {
	AWSDefaults bool `json:"aws-defaults"`
	AWSService  bool `json:"aws-service"`
	OtherIaC    bool `json:"other-iac"`
	Derived     bool `json:"derived"`
	Ephemeral   bool `json:"ephemeral"`
}

// Audit configures the backend audit.
type Audit struct {
	Policy   AuditPolicy `json:"policy"`
	Suppress []Object    `json:"suppress" jsonschema_description:"Suppressions, each with a reason and an optional expiry."`
}

// AuditPolicy holds the audit thresholds.
type AuditPolicy struct {
	MinTerraformVersion string `json:"min_terraform_version" jsonschema:"minLength=1"`
	NoncurrentDays      int    `json:"noncurrent_days"       jsonschema:"minimum=0"`
	StaleLockHours      int    `json:"stale_lock_hours"      jsonschema:"minimum=0"`
}

// Drift configures drift detection.
type Drift struct {
	Mode     string   `json:"mode"     jsonschema:"enum=refresh-only,enum=full"`
	Collapse string   `json:"collapse" jsonschema:"enum=shape,enum=strict,enum=none"`
	Noise    []Object `json:"noise"                                                  jsonschema_description:"Noise rules."`
}

// Cache configures the results cache.
type Cache struct {
	TTL string `json:"ttl" jsonschema:"pattern=^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$" jsonschema_description:"How long cached inventory stays fresh, as a Go duration such as 1h."`
}

const (
	defaultNoncurrentDays = 30
	defaultStaleLockHours = 24
)

// Default returns the built-in configuration. Slices and maps are non-nil so that
// config show lists every key.
func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Binaries:      Binaries{Terragrunt: "terragrunt", Terraform: "terraform"},
		Discovery:     Discovery{Roots: []string{}, GenerateStacks: false, Render: "auto"},
		Accounts: Accounts{
			Profiles: []string{},
			Org:      Org{Role: "OrganizationAccountAccessRole"},
			Map:      map[string]AccountRef{},
		},
		Regions: []string{},
		Scan: Scan{
			Layers:            "auto",
			CloudControlTypes: []string{},
			Packs: Packs{
				AWSDefaults: true,
				AWSService:  true,
				OtherIaC:    true,
				Derived:     true,
				Ephemeral:   true,
			},
			NeverHide: []string{},
		},
		Rules: []Object{},
		Audit: Audit{
			Policy: AuditPolicy{
				MinTerraformVersion: "1.10",
				NoncurrentDays:      defaultNoncurrentDays,
				StaleLockHours:      defaultStaleLockHours,
			},
			Suppress: []Object{},
		},
		Drift:  Drift{Mode: "refresh-only", Collapse: "shape", Noise: []Object{}},
		Notify: Object{},
		Cache:  Cache{TTL: "1h"},
	}
}

func toMap(c Config) (map[string]any, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return m, nil
}
