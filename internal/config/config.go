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
// notify is free-form until the milestone that owns it defines its shape.
type Config struct {
	SchemaVersion int       `json:"schema_version" jsonschema:"enum=1" jsonschema_description:"Config file format version."`
	Binaries      Binaries  `json:"binaries"`
	Discovery     Discovery `json:"discovery"`
	Accounts      Accounts  `json:"accounts"`
	Regions       []string  `json:"regions"                            jsonschema_description:"Regions to scan. Empty means the regions found in the units' backends and providers."`
	Scan          Scan      `json:"scan"`
	Rules         []Rule    `json:"rules"                              jsonschema_description:"Ownership rules, tried in order after the built-in packs. The first rule that matches an unmanaged resource sets its category."`
	Audit         Audit     `json:"audit"`
	Drift         Drift     `json:"drift"`
	Notify        Notify    `json:"notify"`
	Cache         Cache     `json:"cache"`
}

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
	Enabled           bool              `json:"enabled"            jsonschema_description:"List the accounts of the organization and assume a role in each."`
	ManagementProfile string            `json:"management_profile" jsonschema_description:"Profile that lists the accounts. Empty means the default AWS credentials."`
	Role              string            `json:"role"               jsonschema_description:"Role assumed in each member account."                                                  jsonschema:"minLength=1"`
	OURoles           map[string]string `json:"ou_roles"           jsonschema_description:"OU or root ID to the role assumed in the accounts directly under it, instead of role."`
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
	NeverHide         []string `json:"never_hide"         jsonschema_description:"Categories that user rules can't give to guarded resources (IAM users)."         jsonschema:"enum=other-iac,enum=aws-managed,enum=derived,enum=ignored"`
}

// Rule is an ownership rule: a CEL expression over resource and unit.
type Rule struct {
	Name     string `json:"name"     jsonschema:"required,minLength=1,pattern=^[A-Za-z0-9][A-Za-z0-9._-]*$"          jsonschema_description:"Rule name, shown in findings as user/<name>."`
	Category string `json:"category" jsonschema:"required,enum=other-iac,enum=aws-managed,enum=derived,enum=ignored" jsonschema_description:"Category of the resources the rule matches."`
	Owner    string `json:"owner"                                                                                    jsonschema_description:"Who owns the matched resources, such as datadog."`
	Match    string `json:"match"    jsonschema:"required,minLength=1"                                               jsonschema_description:"CEL expression that returns a bool, over resource and unit."`
	Reason   string `json:"reason"   jsonschema:"required,minLength=1"                                               jsonschema_description:"Why the matched resources aren't unmanaged."`
	Expires  string `json:"expires"  jsonschema:"pattern=^[0-9]{4}-[0-9]{2}-[0-9]{2}$"                               jsonschema_description:"Last day the rule applies, as a quoted \"YYYY-MM-DD\" date (UTC). After it the rule stops matching and scan warns."`
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
	Policy           AuditPolicy   `json:"policy"`
	DiscoverPatterns []string      `json:"discover_patterns" jsonschema_description:"Bucket name globs that audit backend --discover audits, matched against every bucket of each account."`
	Suppress         []Suppression `json:"suppress"          jsonschema_description:"Suppressions. A finding matching every set field of an entry is suppressed until expires."`
}

// AuditPolicy holds the audit thresholds.
type AuditPolicy struct {
	MinTerraformVersion string            `json:"min_terraform_version" jsonschema:"minLength=1" jsonschema_description:"FL-TFVER flags states written by an older terraform or tofu."`
	MinProviderVersions map[string]string `json:"min_provider_versions"                          jsonschema_description:"FL-PROV: provider source (hashicorp/aws) to the oldest version allowed in .terraform.lock.hcl."`
	NoncurrentDays      int               `json:"noncurrent_days"       jsonschema:"minimum=0"   jsonschema_description:"S3-LC: noncurrent state versions must be kept at least this many days."`
	StaleLockHours      int               `json:"stale_lock_hours"      jsonschema:"minimum=0"   jsonschema_description:"LK-STALE flags locks older than this."`
	MaxStateMB          float64           `json:"max_state_mb"          jsonschema:"minimum=0"   jsonschema_description:"FL-SIZE flags states larger than this many MiB."`
}

// Suppression hides audit findings. Empty match fields match everything.
type Suppression struct {
	Check   string `json:"check"   jsonschema_description:"Check ID or glob, such as S3-MFA or LK-*."`
	Bucket  string `json:"bucket"  jsonschema_description:"Bucket name glob."`
	Unit    string `json:"unit"    jsonschema_description:"Unit glob, repo/path."`
	Key     string `json:"key"     jsonschema_description:"State key or lock table glob."`
	Reason  string `json:"reason"  jsonschema_description:"Why the finding is accepted."                                                                                             jsonschema:"required,minLength=1"`
	Expires string `json:"expires" jsonschema_description:"Last day the suppression applies, as a quoted \"YYYY-MM-DD\" date (UTC). After it the finding comes back with a warning." jsonschema:"pattern=^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
}

// Drift configures drift detection.
type Drift struct {
	Mode      string      `json:"mode"       jsonschema:"enum=refresh-only,enum=full"                      jsonschema_description:"refresh-only reports drift; full also tells drift the plan reverts from drift it keeps."`
	Collapse  string      `json:"collapse"   jsonschema:"enum=shape,enum=strict,enum=none"                 jsonschema_description:"How identical changes in several units become one group."`
	NeverHide []string    `json:"never_hide" jsonschema:"enum=create,enum=update,enum=delete,enum=replace" jsonschema_description:"Drift actions that no noise rule can hide."`
	Noise     []NoiseRule `json:"noise"                                                                    jsonschema_description:"Noise rules, tried in order. A rule hides the matching attribute changes, or the whole change when it has no attribute."`
}

// Notify configures the notifiers. Secrets never go here: each notifier names the environment
// variable that holds its token or webhook URL.
type Notify struct {
	Enabled            []string        `json:"enabled"              jsonschema:"enum=slack,enum=github-issues,enum=github-pr-comment,enum=bitbucket-pr-comment,enum=file" jsonschema_description:"Notifiers tfcensus notify runs when --notify isn't given."`
	Categories         []string        `json:"categories"           jsonschema:"enum=double-managed,enum=unmanaged,enum=undetermined"                                     jsonschema_description:"Scan categories the notifiers report."`
	MinSeverity        string          `json:"min_severity"         jsonschema:"enum=info,enum=low,enum=medium,enum=high"                                                 jsonschema_description:"Lowest backend audit severity the notifiers report."`
	Top                int             `json:"top"                  jsonschema:"minimum=1,maximum=50"                                                                     jsonschema_description:"Findings listed in a Slack message."`
	Slack              SlackNotify     `json:"slack"`
	GitHubIssues       GitHubNotify    `json:"github_issues"`
	GitHubPRComment    GitHubPRNotify  `json:"github_pr_comment"`
	BitbucketPRComment BitbucketNotify `json:"bitbucket_pr_comment"`
	File               FileNotify      `json:"file"`
}

// SlackNotify configures the Slack incoming webhook notifier.
type SlackNotify struct {
	WebhookEnv string `json:"webhook_env" jsonschema:"pattern=^[A-Za-z_][A-Za-z0-9_]*$" jsonschema_description:"Environment variable that holds the incoming webhook URL."`
	When       string `json:"when"        jsonschema:"enum=findings,enum=always"        jsonschema_description:"findings sends only when there is something to report; always sends every run."`
	Link       string `json:"link"        jsonschema:"pattern=^(https://.*)?$"          jsonschema_description:"URL of the full report, such as a CI artifact, shown in the message."`
}

// GitHubNotify configures the GitHub issues notifier.
type GitHubNotify struct {
	Repo          string   `json:"repo"           jsonschema:"pattern=^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)?$" jsonschema_description:"owner/name. Empty means GITHUB_REPOSITORY."`
	APIURL        string   `json:"api_url"        jsonschema:"pattern=^(https?://.*)?$"                     jsonschema_description:"REST API base URL. Empty means GITHUB_API_URL, else https://api.github.com."`
	TokenEnv      string   `json:"token_env"      jsonschema:"pattern=^[A-Za-z_][A-Za-z0-9_]*$"             jsonschema_description:"Environment variable that holds the token (issues: write)."`
	Labels        []string `json:"labels"                                                                   jsonschema_description:"Labels added to new issues, besides tfcensus and tfcensus:<kind>."`
	MaxOpen       int      `json:"max_open"       jsonschema:"minimum=1"                                    jsonschema_description:"Most open issues per report kind. Further scopes are listed in the run output and the summary issue."`
	CloseResolved bool     `json:"close_resolved"                                                           jsonschema_description:"Close an issue when a run covers its scope and finds nothing."`
	Summary       bool     `json:"summary"                                                                  jsonschema_description:"Keep one summary issue per report kind."`
}

// GitHubPRNotify configures the GitHub pull request comment notifier.
type GitHubPRNotify struct {
	Repo        string `json:"repo"         jsonschema:"pattern=^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)?$" jsonschema_description:"owner/name. Empty means GITHUB_REPOSITORY."`
	APIURL      string `json:"api_url"      jsonschema:"pattern=^(https?://.*)?$"                     jsonschema_description:"REST API base URL. Empty means GITHUB_API_URL, else https://api.github.com."`
	PullRequest int    `json:"pull_request" jsonschema:"minimum=0"                                    jsonschema_description:"Pull request number. 0 means the pull request of the GITHUB_EVENT_PATH event."`
	TokenEnv    string `json:"token_env"    jsonschema:"pattern=^[A-Za-z_][A-Za-z0-9_]*$"             jsonschema_description:"Environment variable that holds the token (pull-requests: write)."`
}

// BitbucketNotify configures the Bitbucket Cloud pull request comment notifier.
type BitbucketNotify struct {
	Workspace   string `json:"workspace"    jsonschema_description:"Workspace. Empty means BITBUCKET_WORKSPACE."`
	Repo        string `json:"repo"         jsonschema_description:"Repository slug. Empty means BITBUCKET_REPO_SLUG."`
	PullRequest int    `json:"pull_request" jsonschema_description:"Pull request ID. 0 means BITBUCKET_PR_ID."                                                                                                          jsonschema:"minimum=0"`
	APIURL      string `json:"api_url"      jsonschema_description:"REST API base URL."                                                                                                                                 jsonschema:"pattern=^https?://"`
	TokenEnv    string `json:"token_env"    jsonschema_description:"Environment variable that holds an access token or API token."                                                                                      jsonschema:"pattern=^[A-Za-z_][A-Za-z0-9_]*$"`
	EmailEnv    string `json:"email_env"    jsonschema_description:"Environment variable that holds the Atlassian account email. Set it to send an API token with basic auth; empty sends the token as a bearer token." jsonschema:"pattern=^([A-Za-z_][A-Za-z0-9_]*)?$"`
}

// FileNotify configures the file notifier.
type FileNotify struct {
	Dir     string   `json:"dir"     jsonschema:"minLength=1"       jsonschema_description:"Directory for tfcensus-<kind>.md and .json."`
	Formats []string `json:"formats" jsonschema:"enum=md,enum=json" jsonschema_description:"Files to write."`
}

// NoiseRule hides drift that matches every set selector. An entry is either a preset or a rule.
type NoiseRule struct {
	Preset    string `json:"preset"    jsonschema:"enum=aws-tags-all,enum=computed-hashes"           jsonschema_description:"A built-in rule. A preset entry takes no other key."`
	Name      string `json:"name"      jsonschema:"pattern=^[A-Za-z0-9][A-Za-z0-9._-]*$"             jsonschema_description:"Rule name, shown on hidden drift as user/<name>."`
	Unit      string `json:"unit"                                                                    jsonschema_description:"Unit path glob; * matches any characters, / included."`
	Type      string `json:"type"                                                                    jsonschema_description:"Resource type glob, such as aws_s3_*."`
	Address   string `json:"address"                                                                 jsonschema_description:"Resource address glob, such as module.tags.*."`
	Attribute string `json:"attribute"                                                               jsonschema_description:"Attribute path such as tags_all or ingress[*].cidr_blocks. It also matches everything below it."`
	Action    string `json:"action"    jsonschema:"enum=create,enum=update,enum=delete,enum=replace" jsonschema_description:"Drift action."`
	Reason    string `json:"reason"                                                                  jsonschema_description:"Why the drift is noise. Required for rules."`
	Expires   string `json:"expires"   jsonschema:"pattern=^[0-9]{4}-[0-9]{2}-[0-9]{2}$"             jsonschema_description:"Last day the rule applies, as a quoted \"YYYY-MM-DD\" date (UTC). After it the rule stops hiding drift and drift warns."`
}

// Cache configures the results cache.
type Cache struct {
	TTL string `json:"ttl" jsonschema:"pattern=^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$" jsonschema_description:"How long cached inventory stays fresh, as a Go duration such as 1h."`
}

const (
	defaultNoncurrentDays = 30
	defaultStaleLockHours = 24
	defaultMaxStateMB     = 20
	defaultNotifyTop      = 10
	defaultMaxOpen        = 10
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
			Org:      Org{Role: "OrganizationAccountAccessRole", OURoles: map[string]string{}},
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
		Rules: []Rule{},
		Audit: Audit{
			Policy: AuditPolicy{
				MinTerraformVersion: "1.10",
				MinProviderVersions: map[string]string{},
				NoncurrentDays:      defaultNoncurrentDays,
				StaleLockHours:      defaultStaleLockHours,
				MaxStateMB:          defaultMaxStateMB,
			},
			DiscoverPatterns: []string{"*tfstate*", "*terraform-state*", "*tf-state*"},
			Suppress:         []Suppression{},
		},
		Drift: Drift{
			Mode: "refresh-only", Collapse: "shape", NeverHide: []string{"delete", "replace"}, Noise: []NoiseRule{},
		},
		Notify: Notify{
			Enabled:     []string{},
			Categories:  []string{"double-managed", "unmanaged"},
			MinSeverity: "low",
			Top:         defaultNotifyTop,
			Slack:       SlackNotify{WebhookEnv: "SLACK_WEBHOOK_URL", When: "findings"},
			GitHubIssues: GitHubNotify{ //nolint:gosec // TokenEnv names a variable; it holds no secret
				TokenEnv: "GITHUB_TOKEN", Labels: []string{}, MaxOpen: defaultMaxOpen, CloseResolved: true,
			},
			GitHubPRComment:    GitHubPRNotify{TokenEnv: "GITHUB_TOKEN"}, //nolint:gosec // TokenEnv names a variable
			BitbucketPRComment: BitbucketNotify{APIURL: "https://api.bitbucket.org/2.0", TokenEnv: "BITBUCKET_TOKEN"},
			File:               FileNotify{Dir: ".", Formats: []string{"md", "json"}},
		},
		Cache: Cache{TTL: "1h"},
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
