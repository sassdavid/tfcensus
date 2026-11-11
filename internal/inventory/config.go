package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"strings"

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
	Tags         []struct {
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
	return "SELECT resourceId, resourceName, resourceType, arn, awsRegion, accountId, tags, relationships" +
		" WHERE resourceType IN (" + strings.Join(quoted, ", ") + ")"
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
	var row configRow
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		return CloudResource{}, fmt.Errorf("decode config result: %w", err)
	}
	r := CloudResource{
		Account: row.AccountID, Region: row.AWSRegion, CFNType: l.env.Map.FromConfig(row.ResourceType),
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
	for _, rel := range row.Relationships {
		if strings.HasPrefix(rel.RelationshipName, "Is contained in") {
			r.Parents = append(r.Parents, Ref{CFNType: l.env.Map.FromConfig(rel.ResourceType), ID: rel.ResourceID})
		}
	}
	return r, nil
}
