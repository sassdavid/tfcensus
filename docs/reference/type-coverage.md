---
description: Which Terraform resource types tfcensus maps to a CloudFormation type, which inventory layer lists each, and which types stay undetermined.
---

# Type mapping coverage

`scan` and `whois` match a live resource with a state resource through the type mapping: a
Terraform type, the CloudFormation type that AWS Config, Resource Explorer and Cloud Control report,
and the keys to match on. A type with no mapping is `undetermined`. tfcensus never counts it as
managed, because it can't tell which state could manage it.

The table lists every resource type of the AWS provider. A mapped type shows its CloudFormation
type, the inventory layers that list it with the default config, and its match keys in the order
matching tries them: identity, then ARN, then ID. A type that no layer lists is still mapped for
`whois`, which looks resources up by ARN or ID.

<TypeCoverage />

`scan --explain-coverage` prints the same facts for your own accounts: which layer listed each type
in each account and region, and the AWS Config types that `scan` doesn't map, with counts.

## Inventory layers

<!--@include: ../parts/inventory-layers.md-->
