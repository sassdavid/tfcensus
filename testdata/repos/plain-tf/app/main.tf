variable "region" {
  type    = string
  default = "eu-west-1"
}

provider "aws" {
  region              = var.region
  allowed_account_ids = ["666666666666"]
}

resource "aws_sqs_queue" "jobs" {
  name = "jobs"
}
