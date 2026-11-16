package inventory

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"golang.org/x/sync/errgroup"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// LayerStacks is the [Run] layer name of the CloudFormation stack listings. It isn't in
// [LayerOrder]: stacks say who owns a resource, they don't list resources.
const LayerStacks = "cloudformation"

// StackResource is one resource that a CloudFormation stack lists. Only identifiers are read,
// never templates, parameters or outputs.
type StackResource struct {
	Account    string `json:"account"`
	Region     string `json:"region"`
	CFNType    string `json:"cfn_type"`
	PhysicalID string `json:"physical_id"`
	LogicalID  string `json:"logical_id"`
	Stack      string `json:"stack"`
	StackID    string `json:"stack_id"`
	Root       string `json:"root,omitempty"` // the top-level stack's name, for a nested stack
}

// StackResult is what [Collector.Stacks] found: the stack resources, and one [Run] per account
// and region with [LayerStacks] as its layer.
type StackResult struct {
	Resources []StackResource
	Runs      []Run
}

// liveStackStatuses are the stack statuses ListStacks asks for: every status but
// DELETE_COMPLETE, whose stacks own nothing, and REVIEW_IN_PROGRESS, whose stacks have no
// resources yet. The list is spelled out so the request body doesn't change with the SDK.
func liveStackStatuses() []cfntypes.StackStatus {
	return []cfntypes.StackStatus{
		"CREATE_COMPLETE", "CREATE_FAILED", "CREATE_IN_PROGRESS",
		"DELETE_FAILED", "DELETE_IN_PROGRESS",
		"IMPORT_COMPLETE", "IMPORT_IN_PROGRESS", "IMPORT_ROLLBACK_COMPLETE", "IMPORT_ROLLBACK_FAILED",
		"IMPORT_ROLLBACK_IN_PROGRESS",
		"ROLLBACK_COMPLETE", "ROLLBACK_FAILED", "ROLLBACK_IN_PROGRESS",
		"UPDATE_COMPLETE", "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS", "UPDATE_FAILED", "UPDATE_IN_PROGRESS",
		"UPDATE_ROLLBACK_COMPLETE", "UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS", "UPDATE_ROLLBACK_FAILED",
		"UPDATE_ROLLBACK_IN_PROGRESS",
	}
}

// goneResource reports whether a stack resource no longer belongs to its stack: deleted, or
// skipped on delete because of a Retain policy.
func goneResource(s cfntypes.ResourceStatus) bool {
	return s == cfntypes.ResourceStatusDeleteComplete || s == cfntypes.ResourceStatusDeleteSkipped
}

// Stacks lists the resources of every CloudFormation stack in each target's probed regions with
// cloudformation:ListStacks and cloudformation:ListStackResources. A failed listing is a [Run]
// with its error; Stacks itself fails only when ctx is done.
func (c *Collector) Stacks(ctx context.Context, targets []Target) (*StackResult, error) {
	sorted := slices.Clone(targets)
	slices.SortFunc(sorted, func(a, b Target) int { return strings.Compare(a.Account.ID, b.Account.ID) })
	type slot struct {
		acct  *accounts.Account
		run   Run
		found []StackResource
	}
	var slots []*slot
	for _, t := range sorted {
		regions := make([]string, 0, len(t.Report.Regions))
		for _, r := range t.Report.Regions {
			regions = append(regions, r.Region)
		}
		slices.Sort(regions)
		for _, region := range slices.Compact(regions) {
			slots = append(
				slots,
				&slot{acct: t.Account, run: Run{Account: t.Account.ID, Region: region, Layer: LayerStacks}},
			)
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.opts.Concurrency)
	for _, s := range slots {
		g.Go(func() error {
			s.found = c.listStacks(gctx, s.acct, s.run.Region, &s.run)
			return nil
		})
	}
	_ = g.Wait()
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	res := &StackResult{Resources: []StackResource{}, Runs: make([]Run, 0, len(slots))}
	for _, s := range slots {
		res.Runs = append(res.Runs, s.run)
		res.Resources = append(res.Resources, s.found...)
	}
	return res, nil
}

// listStacks lists one account and region and records the outcome on run.
func (c *Collector) listStacks(ctx context.Context, acct *accounts.Account, region string, run *Run) []StackResource {
	var found []StackResource
	denied, failed := 0, 0
	for r, err := range c.stackResources(ctx, acct, region) {
		if err != nil {
			run.Errors = append(run.Errors, err.Error())
			if Denied(err) {
				denied++
			} else {
				failed++
			}
			continue
		}
		found = append(found, r)
	}
	run.Count = len(found)
	switch {
	case denied+failed == 0:
		run.Status = RunOK
	case len(found) > 0:
		run.Status = RunPartial
	case failed == 0:
		run.Status = RunDenied
	default:
		run.Status = RunError
	}
	if run.Status != RunOK {
		c.opts.Env.logger().WarnContext(ctx, "CloudFormation stack listing failed", "account", acct.ID,
			"region", region, "status", string(run.Status), "errors", len(run.Errors))
	}
	return found
}

// stackResources yields the resources of every live stack. A failed ListStacks page ends the
// listing; a failed ListStackResources skips that stack only.
func (c *Collector) stackResources(
	ctx context.Context,
	acct *accounts.Account,
	region string,
) iter.Seq2[StackResource, error] {
	return func(yield func(StackResource, error) bool) {
		client := cloudformation.NewFromConfig(acct.Config, func(o *cloudformation.Options) {
			o.Region = region
			o.Retryer = accounts.AdaptiveRetryer(acct.Config)
			o.APIOptions = append(o.APIOptions, c.opts.Env.Limits.APIOption(acct.ID, region, ServiceCFN))
		})
		stacks := cloudformation.NewListStacksPaginator(client, &cloudformation.ListStacksInput{
			StackStatusFilter: liveStackStatuses(),
		})
		var summaries []cfntypes.StackSummary
		for stacks.HasMorePages() {
			page, err := stacks.NextPage(ctx)
			if err != nil {
				yield(StackResource{}, fmt.Errorf("cloudformation:ListStacks: %w", err))
				return
			}
			summaries = append(summaries, page.StackSummaries...)
		}
		slices.SortFunc(summaries, func(a, b cfntypes.StackSummary) int {
			return strings.Compare(aws.ToString(a.StackId), aws.ToString(b.StackId))
		})
		for i := range summaries {
			if !c.oneStack(ctx, client, acct, region, &summaries[i], yield) {
				return
			}
		}
	}
}

// oneStack yields the resources of one stack and reports whether to go on.
func (c *Collector) oneStack(
	ctx context.Context,
	client *cloudformation.Client,
	acct *accounts.Account,
	region string,
	s *cfntypes.StackSummary,
	yield func(StackResource, error) bool,
) bool {
	id := aws.ToString(s.StackId)
	base := StackResource{
		Account: acct.ID, Region: region, Stack: aws.ToString(s.StackName), StackID: id,
		Root: stackName(aws.ToString(s.RootId)),
	}
	pages := cloudformation.NewListStackResourcesPaginator(client, &cloudformation.ListStackResourcesInput{
		StackName: aws.String(id),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return yield(StackResource{}, fmt.Errorf("cloudformation:ListStackResources %s: %w", base.Stack, err))
		}
		for _, sr := range page.StackResourceSummaries {
			phys := aws.ToString(sr.PhysicalResourceId)
			if phys == "" || goneResource(sr.ResourceStatus) {
				continue
			}
			r := base
			r.CFNType, r.LogicalID, r.PhysicalID = aws.ToString(
				sr.ResourceType,
			), aws.ToString(
				sr.LogicalResourceId,
			), phys
			if !yield(r, nil) {
				return false
			}
		}
	}
	return true
}

// stackName returns the name in a stack ARN (arn:aws:cloudformation:region:account:stack/name/uuid).
func stackName(stackID string) string {
	a, err := awsarn.Parse(stackID)
	if err != nil {
		return ""
	}
	parts := strings.Split(a.Resource, "/")
	if len(parts) < 2 { //nolint:mnd // stack/name
		return ""
	}
	return parts[1]
}

// StackIndex finds the stack resource behind a live resource. The physical ID of a stack
// resource is, depending on the type, the live resource's ID, its name, its ARN or the ARN's last
// segment.
type StackIndex struct {
	byKey map[string][]*StackResource
}

// NewStackIndex indexes stack resources, which must outlive the index.
func NewStackIndex(resources []StackResource) *StackIndex {
	ix := &StackIndex{byKey: map[string][]*StackResource{}}
	for i := range resources {
		r := &resources[i]
		phys := r.PhysicalID
		if strings.HasPrefix(phys, "arn:") {
			phys = typemap.NormalizeARN(phys)
		}
		// Account-wide types (IAM) have no region in the inventory, but a stack in any region can
		// own them.
		for _, region := range []string{r.Region, ""} {
			k := stackKey(r.Account, region, r.CFNType, phys)
			ix.byKey[k] = append(ix.byKey[k], r)
		}
	}
	for _, rs := range ix.byKey {
		slices.SortFunc(rs, func(a, b *StackResource) int {
			return cmp.Or(strings.Compare(a.StackID, b.StackID), strings.Compare(a.LogicalID, b.LogicalID))
		})
	}
	return ix
}

func stackKey(account, region, cfn, phys string) string {
	return account + "\x00" + region + "\x00" + cfn + "\x00" + phys
}

// Find returns the stack resource of r, or nil. It tries r's ID, ARN, name and the ARN's last
// segment, in that order, among stack resources of r's account, region and CloudFormation type.
func (ix *StackIndex) Find(r *CloudResource) *StackResource {
	if ix == nil || r.CFNType == "" {
		return nil
	}
	for _, v := range []string{r.ID, r.ARN, r.Name, arnTail(r.ARN)} {
		if v == "" {
			continue
		}
		if rs := ix.byKey[stackKey(r.Account, r.Region, r.CFNType, v)]; len(rs) > 0 {
			return rs[0]
		}
	}
	return nil
}

// arnTail returns what follows the last "/" or ":" of an ARN's resource, such as a topic's name.
func arnTail(arn string) string {
	a, err := awsarn.Parse(arn)
	if err != nil {
		return ""
	}
	return a.Resource[strings.LastIndexAny(a.Resource, "/:")+1:]
}
