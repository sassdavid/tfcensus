package inventory

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/probe"
)

// Aggregator is the AWS Config aggregator the config layer reads instead of querying each
// account and region.
type Aggregator struct {
	Account *accounts.Account // the account that owns the aggregator
	Report  *probe.AggregatorReport
}

// AggregatorRun is the outcome of the aggregator queries.
type AggregatorRun struct {
	Name     string    `json:"name"`
	Account  string    `json:"account"`
	Region   string    `json:"region"`
	Status   RunStatus `json:"status"`
	Count    int       `json:"count"`    // rows read, before they are matched to accounts and regions
	Served   int       `json:"served"`   // account and region pairs the aggregator listed
	Fallback int       `json:"fallback"` // pairs listed per account after all, because the aggregator returned nothing for them
	Errors   []string  `json:"errors,omitempty"`
}

// ViaAggregator marks a [Run] whose resources came from the aggregator.
const ViaAggregator = "aggregator"

// maxExpression is the longest SelectAggregateResourceConfig expression AWS accepts.
const maxExpression = 4096

// pairKey identifies an account and region.
type pairKey struct{ account, region string }

// aggPlan is where the aggregate rows go and what the queries ask for.
type aggPlan struct {
	byPair  map[pairKey]*task
	global  map[string]*task // per account, the task that takes global rows (IAM)
	accts   []string         // sorted
	regions []string         // sorted, with the region Config gives global resources when a task takes them
}

func newAggPlan(tasks []*task) *aggPlan {
	p := &aggPlan{byPair: map[pairKey]*task{}, global: map[string]*task{}}
	for _, t := range tasks {
		p.byPair[pairKey{t.run.Account, t.region}] = t
		p.accts = appendMissing(p.accts, []string{t.run.Account})
		p.regions = appendMissing(p.regions, []string{t.region})
		if _, ok := p.global[t.run.Account]; !ok && t.caps.ConfigGlobal {
			p.global[t.run.Account] = t
			p.regions = appendMissing(p.regions, []string{globalRegion})
		}
	}
	slices.Sort(p.accts)
	slices.Sort(p.regions)
	return p
}

// add puts r in its task's slot. A row of a pair the aggregator doesn't serve is dropped.
func (p *aggPlan) add(r CloudResource) {
	t := p.byPair[pairKey{r.Account, r.Region}]
	if r.Region == "" {
		t = p.global[r.Account]
	}
	if t != nil {
		t.found = append(t.found, r)
	}
}

// aggregate lists the config tasks through the aggregator and fills their slots. It returns the
// tasks the aggregator returned nothing for, which the caller lists per account.
func (c *Collector) aggregate(ctx context.Context, agg *Aggregator, tasks []*task, types []string) (
	*AggregatorRun, []*task,
) {
	rep := agg.Report
	run := &AggregatorRun{Name: rep.Name, Account: agg.Account.ID, Region: rep.Region, Status: RunOK}
	plan := newAggPlan(tasks)
	type query struct {
		text  string
		accts []string
	}
	var queries []query
	for _, typeChunk := range chunkTypes(c.opts.Env.Map.ConfigTypes(types)) {
		chunks, err := chunkAccounts(typeChunk, plan.accts, plan.regions)
		if err != nil {
			run.Status, run.Errors = RunError, []string{err.Error()}
			return run, tasks
		}
		for _, chunk := range chunks {
			queries = append(queries, query{aggregateQuery(typeChunk, chunk, plan.regions), chunk})
		}
	}
	client := configservice.NewFromConfig(agg.Account.Config, func(o *configservice.Options) {
		o.Region = rep.Region
		o.Retryer = accounts.AdaptiveRetryer(agg.Account.Config)
		o.APIOptions = append(o.APIOptions, c.opts.Env.Limits.APIOption(agg.Account.ID, rep.Region, ServiceConfig))
	})
	failed := map[string]bool{}
	failedQueries, denied := 0, 0
	for _, query := range queries {
		rows, qerr := c.selectAggregate(ctx, client, rep.Name, query.text)
		if qerr != nil {
			run.Errors = append(run.Errors, qerr.Error())
			failedQueries++
			if Denied(qerr) {
				denied++
			}
			for _, a := range query.accts {
				failed[a] = true
			}
			continue
		}
		run.Count += len(rows)
		for _, raw := range rows {
			r, derr := DecodeConfigItem(c.opts.Env.Map, []byte(raw))
			if derr != nil {
				run.Errors = append(run.Errors, derr.Error())
				continue
			}
			plan.add(r)
		}
	}
	fallback := settle(run, tasks, failed)
	run.Status = aggStatus(len(queries), failedQueries, denied, run.Errors)
	if run.Status != RunOK {
		c.opts.Env.logger().WarnContext(ctx, "config aggregator query failed; listing those accounts one by one",
			"aggregator", rep.Name, "status", string(run.Status), "errors", len(run.Errors))
	}
	return run, fallback
}

// settle marks the tasks the aggregator listed and returns the others: those of a failed query
// and those it returned nothing for.
func settle(run *AggregatorRun, tasks []*task, failed map[string]bool) []*task {
	var fallback []*task
	for _, t := range tasks {
		if failed[t.run.Account] || len(t.found) == 0 {
			t.found = nil
			fallback = append(fallback, t)
			continue
		}
		t.run.Status, t.run.Count, t.run.Via = RunOK, len(t.found), ViaAggregator
		run.Served++
	}
	run.Fallback = len(fallback)
	return fallback
}

// aggStatus is the outcome of the queries: denied or error when every query failed, partial when
// some query or row failed.
func aggStatus(queries, failedQueries, denied int, errs []string) RunStatus {
	switch {
	case failedQueries == queries && denied == queries:
		return RunDenied
	case failedQueries == queries:
		return RunError
	case len(errs) > 0:
		return RunPartial
	}
	return RunOK
}

func (c *Collector) selectAggregate(ctx context.Context, client *configservice.Client, name, query string) (
	[]string, error,
) {
	var rows []string
	pages := configservice.NewSelectAggregateResourceConfigPaginator(client,
		&configservice.SelectAggregateResourceConfigInput{
			ConfigurationAggregatorName: aws.String(name),
			Expression:                  aws.String(query),
			Limit:                       configPageSize,
		})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("config:SelectAggregateResourceConfig: %w", err)
		}
		rows = append(rows, page.Results...)
	}
	return rows, nil
}

// aggregateQuery is [configQuery] limited to the accounts and regions, all sorted.
func aggregateQuery(configTypes, accts, regions []string) string {
	return configQuery(configTypes) + " AND accountId IN (" + quoteList(accts) + ")" +
		" AND awsRegion IN (" + quoteList(regions) + ")"
}

func quoteList(in []string) string {
	quoted := make([]string, len(in))
	for i, s := range in {
		quoted[i] = "'" + s + "'"
	}
	return strings.Join(quoted, ", ")
}

// chunkAccounts splits accts into the fewest runs whose queries fit in [maxExpression].
func chunkAccounts(configTypes, accts, regions []string) ([][]string, error) {
	var chunks [][]string
	var cur []string
	for _, a := range accts {
		if len(aggregateQuery(configTypes, append(slices.Clone(cur), a), regions)) <= maxExpression {
			cur = append(cur, a)
			continue
		}
		if len(cur) == 0 {
			return nil, fmt.Errorf(
				"config aggregator query for account %s is longer than %d characters",
				a,
				maxExpression,
			)
		}
		chunks = append(chunks, cur)
		cur = []string{a}
		if len(aggregateQuery(configTypes, cur, regions)) > maxExpression {
			return nil, fmt.Errorf(
				"config aggregator query for account %s is longer than %d characters",
				a,
				maxExpression,
			)
		}
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, nil
}
