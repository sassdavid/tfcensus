# Moves the endpoints module and the DNS records between the same workspace of network and
# network-endpoints. Run it with --var ws=<workspace>.
migration "units" "split_workspace" {
  from = "live/network:${var.ws}"
  to = {
    "live/network-endpoints:${var.ws}" = ["module.endpoints", "aws_route53_record.api"]
  }
}
