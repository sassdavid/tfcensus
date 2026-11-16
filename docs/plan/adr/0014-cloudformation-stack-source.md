# tfcensus ADR-0014: CloudFormation stacks as a second IaC source

- Status: accepted (2026-11-16). Resolves open question Q4. Builds on
  [ADR-0003](0003-capability-probed-layered-inventory.md) (inventory layers),
  [ADR-0004](0004-identity-arn-id-matching.md) (matching) and
  [ADR-0010](0010-rules-on-undetermined-and-guards.md) (rules on undetermined resources).

## Context
- Until now scan knew CloudFormation only from the `other-iac` pack rule, which matches the
  `aws:cloudformation:stack-name` and `stack-id` tags. Resources that don't carry those tags
  (CloudFormation doesn't tag every type, and some types have no tags) were `unmanaged`, so
  stacks inflated the unmanaged count. driftctl had the same gap (snyk/driftctl#878).
- `cloudformation:ListStacks` and `ListStackResources` return every stack resource with its type,
  logical ID and physical ID. They return identifiers only. Templates, parameters and outputs need
  other calls, which tfcensus doesn't make.
- The physical ID is, depending on the type, the live resource's ID (`sg-...`, a queue URL), its
  name (an IAM role, an EventBridge rule) or its ARN (an SNS topic). Config and Resource Explorer
  give tfcensus the ID, the name and the ARN.

## Decision
- With `scan.cloudformation` on (the default), scan lists every stack in each probed account and
  region. ListStacks asks for every status but `DELETE_COMPLETE` and `REVIEW_IN_PROGRESS`, spelled
  out in the code so the request doesn't change with the SDK. Resources in `DELETE_COMPLETE` or
  `DELETE_SKIPPED` (retained) and resources without a physical ID are dropped. Nested stacks are
  listed like other stacks, and their `RootId` names the top stack.
- A live resource joins a stack resource with the same account, CloudFormation type and region
  when the physical ID equals its ID, its normalized ARN, its name or the ARN's last segment.
  Account-wide resources (IAM) have no region in the inventory and join a stack in any region.
  A live resource without a CloudFormation type never joins.
- Order: a state match decides first, then the stack, then the ownership rules. A resource that a
  stack lists and no state manages is `other-iac` with owner `cloudformation`, rule
  `source/cloudformation` and a reason naming the stack and the logical ID. This applies to
  undetermined resources too, since the stack names their type. The tag rule stays as the
  fallback for stacks that weren't listed, and for whois.
- A resource that a state manages keeps `managed` or `double-managed`. Its finding carries the
  stack, and the report warns once per such resource.
- The category stays `other-iac`. There is no `managed-by-cfn` category: the milestone's
  `managed-by-cfn` is this rule, and the finding's new optional `stack` object
  (`name`, `id`, `root`, `logical_id`) carries the stack. Adding the object is backwards
  compatible, so `schema_version` stays 1. CSV gets a `stack` column at the end; tty and md list
  the stacks with their resource counts.
- Coverage doesn't change: `other-iac` is outside the KPI, so the number stays the Terraform
  coverage teams track.
- Each account and region gets a `Run` with layer `cloudformation`. A failed or denied listing is
  reported like a failed layer and makes the exit code 1. The client uses adaptive retry and a
  4 requests per second budget per account and region. The stack resources are part of the scan
  snapshot (format 2), and the setting is part of the cache key.
- The read-only IAM policy gets `cloudformation:ListStacks` and `cloudformation:ListStackResources`.
  `doctor` doesn't probe them.
- whois and codify keep the tag rule. Reading stacks there is a follow-up.

## Consequences
- Scans make one ListStacks call per region and page, and one ListStackResources call per stack
  and page. Accounts with many stacks (CDK, StackSets) take longer; `scan.cloudformation: false`
  turns the calls off.
- The join is by identifier, so a type whose physical ID is neither the ID, the name nor the ARN
  in the inventory (an EC2 key pair: its name in CloudFormation, its key pair ID in the ARN)
  doesn't join and falls back to the tag rule.
- A report from before format 2 has no stack data; `report scan` reads only format 2 snapshots.
