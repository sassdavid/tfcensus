# Spec: live cloud inventory layers

```go
type Layer interface {
    Name() string
    Supports(cap Capabilities) bool            // from the capability probe
    Types() []string                           // CFN types it can return here
    List(ctx context.Context, acct Account, region string, types []string) iter.Seq2[CloudResource, error]
}
```

## Layers (merged by ARN/identifier; earlier layers win for attributes)

| Layer | When available | Strengths | Limits |
|---|---|---|---|
| `config` | recorder recording in that region (common under Control Tower) | ~718 types, properties + tags + relationships, `SelectResourceConfig` SQL; org-wide via aggregator in one query | only what the recorder records. Global types (IAM) recorded only in the home region; no recorder in some regions (e.g. us-east-1 in the probed non-prod account) |
| `resource-explorer` | index + a view exists | free, ARN-native, ~649 types live; `ListResources` (no 1,000 cap; MaxResults < 1000; tokens expire 24 h; 5 req/s) | needs a view (we never create one); indexing lag up to 36 h; tags/props minimal |
| `cloudcontrol` | always | ~1,234 listable types | slow and throttled. 439 types need a parent model (e.g. listeners need LoadBalancerArn), resolved through a parent graph from other layers. Used only for types the other layers miss, behind `--layers cc` or the `cloudcontrol_types` allowlist |
| `native/*` | always | precise for awkward types where identity isn't a top-level resource | hand-written; kept small |

Initial `native` enumerators (driftctl's expander lessons):
- `ec2-sg-rules`: `DescribeSecurityGroupRules`, mapping to `aws_vpc_security_group_{ingress,egress}_rule` and to legacy `aws_security_group_rule` / inline rules.
- `iam-attachments`: role, user and group policy attachments, inline policies.
- `route53-records`: `ListResourceRecordSets` per zone (with pagination; dnsReaper's bug #207 is a warning sign).
- `s3-subresources`: bucket policy, versioning, lifecycle, encryption, public access block. These are separate TF resources but bucket attributes in the cloud.

Layer names in a resource's source list: `config`, `resource-explorer`, `native:ec2-sg-rules`,
`native:iam-attachments`, `native:route53-records`, `native:s3-subresources`. IAM and Route 53
listers run once per account in the partition's global region (`us-east-1` for `aws`), never in
whichever region happens to run first. Resource Explorer runs only with a default view.

## Selection
- Default `--layers auto`: config if recording; resource-explorer if a view exists; native always; cloudcontrol only for allowlisted types not covered by the others.
- `--types` limits all layers. A built-in **coverage matrix** (`tfcensus scan --explain-coverage`) shows, per type, which layer covered it in each account and region, so blind spots are visible rather than silent.

## Throughput and cost
- Adaptive retry, plus one client-side token bucket per (account, region, service): Config 2 rps,
  RE 5 rps, EC2 10 rps, S3 10 rps. IAM (5 rps) and Route 53 (5 rps) are global, so their bucket is
  per account. Cloud Control gets 2 rps when its layer lands.
- Results cache per (account, region, layer) for 1 h.
- AWS Config advanced queries are free per the pricing page (only CIs and rule evaluations are billed). Re-verify at M6 and state it in the docs.

## Acceptance criteria
- [x] Each layer has stubbed-response tests (paginated, throttled-then-success, AccessDenied).
- [x] The merge test: the same SG from config + RE + native yields one CloudResource with tags from config and source list `[config, resource-explorer]`.
- [x] The coverage matrix golden test for a 2-region stub where only one region records Config.
- [ ] A parent-graph test: ELBv2 listeners listed via Cloud Control using load balancers found by the config layer.
- [ ] Nightly against emulators: fixture "ClickOps" resources created via SDK in moto (EC2/IAM/Route53/S3) and Floci (Resource Explorer, Cloud Control) are all found by at least one layer. Config `Select*` is covered by L0 fixtures (no emulator runs Config SQL).
