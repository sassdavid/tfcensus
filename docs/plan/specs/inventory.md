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
`native:iam-attachments`, `native:route53-records`, `native:s3-subresources`, `cloudcontrol`. IAM and Route 53
listers run once per account in the partition's global region (`us-east-1` for `aws`), never in
whichever region happens to run first. Resource Explorer runs only with a default view.

## Selection
- Default `--layers auto`: config if recording; resource-explorer if a view exists; native always; cloudcontrol only for allowlisted types not covered by the others.
- Cloud Control runs after the other layers. In auto mode it skips an allowlisted type that another
  layer listed in the same account and region. A type with `cc_parent` in the typemap
  (`AWS::ElasticLoadBalancingV2::Listener` per load balancer, `ListenerRule` per listener) is listed
  once per parent found by an earlier layer or by Cloud Control itself, parents first. The default
  allowlist (`scan.cloudcontrol_types`) is empty.
- `--layers` with names (`config`, `re`, `native`, `cc`, or full layer names) runs exactly those
  layers, without the probe's checks. Naming `cc` lists the whole allowlist; with an empty
  allowlist it lists the `--types`.
- `--types` limits all layers. A layer with none of the wanted types makes no calls (status
  `skipped`). A built-in **coverage matrix** (`tfcensus scan --explain-coverage`) shows, per type, which layer covered it in each account and region, so blind spots are visible rather than silent.

## Throughput and cost
- Adaptive retry, plus one client-side token bucket per (account, region, service): Config 2 rps,
  RE 5 rps, EC2 10 rps, S3 10 rps, Cloud Control 2 rps. IAM (5 rps) and Route 53 (5 rps) are
  global, so their bucket is per account.
- `scan` caches its collected inventory and state index for `cache.ttl` (1 h), keyed by its inputs
  ([ADR-0009](../adr/0009-scan-snapshot-cache.md)), instead of a cache per (account, region, layer).
- AWS Config pricing, checked 2026-11-11 (aws.amazon.com/config/pricing): the page bills recorded
  configuration items, rule evaluations and conformance pack evaluations, and names no charge for
  advanced queries. The advanced query guide names none either.
- The config layer selects `resourceCreationTime` and five configuration flags the ownership packs
  read (`isDefault`, `defaultForAz`, `keyManager`, `requesterManaged`, `interfaceType`), never a
  whole `configuration` document.

## Acceptance criteria
- [x] Each layer has stubbed-response tests (paginated, throttled-then-success, AccessDenied).
- [x] The merge test: the same SG from config + RE + native yields one CloudResource with tags from config and source list `[config, resource-explorer]`.
- [x] The coverage matrix golden test for a 2-region stub where only one region records Config.
- [x] A parent-graph test: ELBv2 listeners listed via Cloud Control using load balancers found by the config layer.
- [x] Nightly against emulators (`TestSpec_inventory_5`, `mise run test:emulator`, `.github/workflows/live.yml`;
  passed locally against moto 5.2.5 and Floci 2.4.0 on 2026-11-11 and in the nightly run 43206897470 on 2026-11-12): fixture "ClickOps" resources created via SDK in moto (EC2/IAM/Route53/S3) and Floci (Resource Explorer, Cloud Control) are all found by at least one layer. Config `Select*` is covered by L0 fixtures (no emulator runs Config SQL).
