# Drafted by tfcensus move suggest from plans. Review every block, then check the file with
# tfcensus move plan <file> before tfcensus move apply.

migration "units" "suggest_1" {
  from = "live/network"
  to = {
    "live/network-endpoints" = [
      "aws_vpc_endpoint.s3",
    ]
    "live/network-sg" = [
      "aws_security_group.app",
      "aws_ssm_parameter.db_password",
      "aws_vpc_security_group_ingress_rule.app[\"http\"]",
      "aws_vpc_security_group_ingress_rule.app[\"https\"]",
    ]
  }
}

migration "units" "suggest_2" {
  from = "live/network"
  actions = [
    "mv aws_vpc.main aws_vpc.this",
  ]
}

migration "units" "suggest_3" {
  from = "live/network-endpoints"
  actions = [
    "mv aws_vpc_endpoint.s3 'module.endpoints.aws_vpc_endpoint.this[\"s3\"]'",
  ]
}
