# Splits the network unit: the app security group and its rules go to network-sg, the
# endpoints module and the DNS records to network-endpoints.
migration "units" "split_network" {
  from = "live/network"
  to = {
    "live/network-sg"        = ["aws_security_group.app", "aws_vpc_security_group_ingress_rule.app[*]"]
    "live/network-endpoints" = ["module.endpoints", "aws_route53_record.api"]
  }
}

migration "units" "rename_vpc" {
  from    = "live/network"
  actions = ["mv aws_vpc.main aws_vpc.this"]
}
