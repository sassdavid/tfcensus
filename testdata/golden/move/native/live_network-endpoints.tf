# Written by tfcensus move from split.hcl, block split_network. Delete it after the apply.

import {
  to = module.endpoints.aws_vpc_endpoint.s3
  id = "vpce-0a1b2c3d4e5f60005"
}

import {
  to = aws_route53_record.api[0]
  id = "Z0FIXTURE0ZONE_api-0.fixture.internal_A"
}

import {
  to = aws_route53_record.api[1]
  id = "Z0FIXTURE0ZONE_api-1.fixture.internal_A"
}
