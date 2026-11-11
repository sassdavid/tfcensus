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
}

func (e *Env) logger() *slog.Logger {
	if e.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return e.Logger
}

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
	// Configuration holds only the flags the ownership rules read; the query selects nothing else.
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
	quoted := make([]string, len(configTypes))
	for i, t := range configTypes {
		quoted[i] = "'" + t + "'"
	}
	return "SELECT resourceId, resourceName, resourceType, arn, awsRegion, accountId, resourceCreationTime, tags," +
		" relationships, " + strings.Join(configFlagFields(), ", ") +
		" WHERE resourceType IN (" + strings.Join(quoted, ", ") + ")"
}

// configFlags maps the configuration properties the ownership rules read to the attribute names
// they get. All are booleans or enums, never values a user wrote.
func configFlags() map[string]string {
	return map[string]string{
		"isDefault":        "is_default",
		"defaultForAz":     "default_for_az",
		"keyManager":       "key_manager",
		"requesterManaged": "requester_managed",
		"interfaceType":    "interface_type",
	}
}

func configFlagFields() []string {
	out := make([]string, 0, len(configFlags()))
	for _, k := range slices.Sorted(maps.Keys(configFlags())) {
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
			o.APIOptions = append(o.APIOptions, l.env.Limits.APIOption(acct.ID, region, ServiceConfig))
		})
		pages := configservice.NewSelectResourceConfigPaginator(client, &configservice.SelectResourceConfigInput{
			Expression: aws.String(configQuery(configTypes)),
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
	if r.Region == "global" {
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
		switch v := row.Configuration[k].(type) {
		case bool:
			r.setAttr(attr, strconv.FormatBool(v))
		case string:
			r.setAttr(attr, v)
		}
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
