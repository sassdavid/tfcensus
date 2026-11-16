package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// Env is what every layer needs.
type Env struct {
	Map         *typemap.Map
	Limits      *Limiters
	Logger      *slog.Logger
	S3PathStyle bool // custom endpoints (emulators) need path-style bucket addressing
	// CloudControlTypes are the CloudFormation types the Cloud Control layer lists.
	CloudControlTypes []string
	// ExternalDNS is the TXT registry naming of the external-dns deployments.
	ExternalDNS ExternalDNS
}

func (e *Env) logger() *slog.Logger {
	if e.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return e.Logger
}

// globalRegion is the region Config and Resource Explorer give global resources such as IAM roles.
const globalRegion = "global"

// configPageSize is the most results SelectResourceConfig returns per page.
const configPageSize = 100

type configLayer struct{ env *Env }

// NewConfig returns the layer that queries the AWS Config recorder with SelectResourceConfig.
// Config records global types (IAM) only in the region whose recorder includes them.
func NewConfig(env *Env) Layer { return &configLayer{env: env} }

func (l *configLayer) Name() string                 { return LayerConfig }
func (l *configLayer) Global() bool                 { return false }
func (l *configLayer) Supports(c Capabilities) bool { return c.Config }

func (l *configLayer) Types() []string {
	return curatedTypes(l.env.Map, func(t *typemap.Type) bool { return t.Config != "" })
}

// configRow is one SelectResourceConfig result.
type configRow struct {
	ResourceID   string `json:"resourceId"`
	ResourceName string `json:"resourceName"`
	ResourceType string `json:"resourceType"`
	ARN          string `json:"arn"`
	AWSRegion    string `json:"awsRegion"`
	AccountID    string `json:"accountId"`
	Created      string `json:"resourceCreationTime"`
	// Configuration holds only the properties the ownership rules read; the query selects nothing
	// else.
	Configuration map[string]any `json:"configuration"`
	Tags          []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"tags"`
	Relationships []struct {
		ResourceID       string `json:"resourceId"`
		ResourceType     string `json:"resourceType"`
		RelationshipName string `json:"relationshipName"`
	} `json:"relationships"`
}

// configQuery builds the query for configTypes, which must be sorted so the request is the same
// on every run.
func configQuery(configTypes []string) string {
	return "SELECT resourceId, resourceName, resourceType, arn, awsRegion, accountId, resourceCreationTime, tags," +
		" relationships, " + strings.Join(configFields(), ", ") +
		" WHERE resourceType IN (" + quoteList(configTypes) + ")"
}

// typeQueryBudget is the longest [configQuery] a chunk of types may give. It leaves room under
// [maxExpression] for the account and region lists of an aggregate query.
const typeQueryBudget = 2816

// chunkTypes splits the sorted configTypes into the fewest runs, in order, whose queries fit in
// [typeQueryBudget].
func chunkTypes(configTypes []string) [][]string {
	var chunks [][]string
	var cur []string
	for _, t := range configTypes {
		if len(cur) > 0 && len(configQuery(append(slices.Clone(cur), t))) > typeQueryBudget {
			chunks = append(chunks, cur)
			cur = nil
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// configFlags maps the scalar configuration properties the ownership rules read to the attribute
// names they get. Most are booleans or enums. The ADR on scan accuracy allows a value a user wrote
// when a rule needs it to tell a service from a person: an ENI's description is kept as is,
// because its prefix is the signal. requesterId is collected for review; no rule reads it yet.
func configFlags() map[string]string {
	return map[string]string{
		"isDefault":                  "is_default",
		"defaultForAz":               "default_for_az",
		"keyManager":                 "key_manager",
		"requesterManaged":           "requester_managed",
		"interfaceType":              "interface_type",
		"description":                "description",
		"requesterId":                "requester_id",
		"attachment.instanceOwnerId": "instance_owner_id",
	}
}

// configLists are the list properties that rules read in reduced form: a route table's
// associations become main.
func configLists() []string { return []string{"associations"} }

// dhcpProperties maps the properties of a DHCP options item to their DHCP option names. AWS Config
// records the type with CloudFormation's property names, not DescribeDhcpOptions' shape.
func dhcpProperties() map[string]string {
	return map[string]string{
		"DomainName":                    "domain-name",
		"DomainNameServers":             "domain-name-servers",
		"NtpServers":                    "ntp-servers",
		"NetbiosNameServers":            "netbios-name-servers",
		"NetbiosNodeType":               "netbios-node-type",
		"Ipv6AddressPreferredLeaseTime": "ipv6-address-preferred-lease-time",
	}
}

func configFields() []string {
	names := slices.Collect(maps.Keys(configFlags()))
	names = append(names, configLists()...)
	names = append(names, slices.Collect(maps.Keys(dhcpProperties()))...)
	slices.Sort(names)
	out := make([]string, 0, len(names))
	for _, k := range names {
		out = append(out, "configuration."+k)
	}
	return out
}

func (l *configLayer) List(
	ctx context.Context,
	acct *accounts.Account,
	region string,
	types []string,
) iter.Seq2[CloudResource, error] {
	return func(yield func(CloudResource, error) bool) {
		configTypes := l.env.Map.ConfigTypes(types)
		if len(configTypes) == 0 {
			return
		}
		client := configservice.NewFromConfig(acct.Config, func(o *configservice.Options) {
			o.Region = region
			o.Retryer = accounts.AdaptiveRetryer(acct.Config)
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceConfig))
		})
		for _, chunk := range chunkTypes(configTypes) {
			pages := configservice.NewSelectResourceConfigPaginator(client, &configservice.SelectResourceConfigInput{
				Expression: aws.String(configQuery(chunk)),
				Limit:      configPageSize,
			})
			for pages.HasMorePages() {
				page, err := pages.NextPage(ctx)
				if err != nil {
					yield(CloudResource{}, fmt.Errorf("config:SelectResourceConfig: %w", err))
					return
				}
				if !l.yieldRows(page.Results, yield) {
					return
				}
			}
		}
	}
}

// yieldRows yields each decoded row, or its decode error, and reports whether to go on.
func (l *configLayer) yieldRows(rows []string, yield func(CloudResource, error) bool) bool {
	for _, raw := range rows {
		if !yield(l.resource(raw)) {
			return false
		}
	}
	return true
}

func (l *configLayer) resource(raw string) (CloudResource, error) {
	return DecodeConfigItem(l.env.Map, []byte(raw))
}

// DecodeConfigItem reads one SelectResourceConfig result row, as the config layer selects it.
func DecodeConfigItem(m *typemap.Map, raw []byte) (CloudResource, error) {
	var row configRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return CloudResource{}, fmt.Errorf("decode config result: %w", err)
	}
	r := CloudResource{
		Account: row.AccountID, Region: row.AWSRegion, CFNType: m.FromConfig(row.ResourceType),
		ARN: typemap.NormalizeARN(row.ARN), ID: row.ResourceID, Name: row.ResourceName,
		Sources: []string{LayerConfig},
	}
	if r.Region == globalRegion {
		r.Region = ""
	}
	for _, t := range row.Tags {
		if r.Tags == nil {
			r.Tags = map[string]string{}
		}
		r.Tags[t.Key] = t.Value
	}
	if t, err := time.Parse(time.RFC3339, row.Created); err == nil {
		r.Created = t.UTC().Format(time.RFC3339)
	}
	for k, attr := range configFlags() {
		switch v := configValue(row.Configuration, k).(type) {
		case bool:
			r.setAttr(attr, strconv.FormatBool(v))
		case string:
			r.setAttr(attr, v)
		}
	}
	if assoc, ok := row.Configuration["associations"].([]any); ok {
		r.setAttr("main", strconv.FormatBool(slices.ContainsFunc(assoc, func(a any) bool {
			m, _ := a.(map[string]any)
			main, _ := m["main"].(bool)
			return main
		})))
	}
	if r.CFNType == cfnDHCPOptions {
		r.setAttr("dhcp_options", dhcpOptions(row.Configuration))
	}
	for _, rel := range row.Relationships {
		ref := Ref{CFNType: m.FromConfig(rel.ResourceType), ID: rel.ResourceID}
		if strings.HasPrefix(rel.RelationshipName, "Is contained in") {
			r.Parents = append(r.Parents, ref)
		}
		ref.Relationship = rel.RelationshipName
		r.Relationships = append(r.Relationships, ref)
	}
	return r, nil
}

// configValue returns the value at a dotted path such as attachment.instanceOwnerId.
func configValue(conf map[string]any, path string) any {
	var cur any = conf
	for part := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

// dhcpOptions renders DHCP options as key=value,value pairs sorted by key and joined by ";", such
// as "domain-name=eu-west-1.compute.internal;domain-name-servers=AmazonProvidedDNS".
func dhcpOptions(conf map[string]any) string {
	var parts []string
	for prop, key := range dhcpProperties() {
		var values []string
		switch v := conf[prop].(type) {
		case string:
			values = []string{v}
		case float64:
			values = []string{strconv.FormatFloat(v, 'f', -1, 64)}
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					values = append(values, s)
				}
			}
		}
		if len(values) > 0 {
			parts = append(parts, key+"="+strings.Join(values, ","))
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ";")
}
