terraform {
  backend "s3" {
    bucket = "acme-audit-bad"
    key    = "legacy/terraform.tfstate"
    region = "eu-west-1"
    dynamodb_table = "acme-audit-locks-bad"
  }
}

provider "aws" {
  region              = "eu-west-1"
  allowed_account_ids = ["111111111111"]
}
