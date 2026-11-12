# Written by tfcensus move from split.hcl, block split_network. Delete it after the apply.

removed {
  from = aws_security_group.app
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_vpc_security_group_ingress_rule.app
  lifecycle {
    destroy = false
  }
}

removed {
  from = module.endpoints.aws_vpc_endpoint.s3
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_route53_record.api
  lifecycle {
    destroy = false
  }
}
