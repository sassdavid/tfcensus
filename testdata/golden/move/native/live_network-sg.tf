# Written by tfcensus move from split.hcl, block split_network. Delete it after the apply.

import {
  to = aws_security_group.app
  identity = {
    account_id = "111111111111"
    id         = "sg-0a1b2c3d4e5f60002"
    region     = "eu-west-1"
  }
}

import {
  to = aws_vpc_security_group_ingress_rule.app["https"]
  id = "sgr-0a1b2c3d4e5f60003"
}

import {
  to = aws_vpc_security_group_ingress_rule.app["ssh"]
  id = "sgr-0a1b2c3d4e5f60004"
}
