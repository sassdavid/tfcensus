terraform {
  backend "s3" {
    bucket = "acme-audit-good"
    key    = "app/terraform.tfstate"
    region = "eu-west-1"
    use_lockfile   = true
    dynamodb_table = "acme-audit-locks-good"
  }
}

provider "aws" {
  region              = "eu-west-1"
  allowed_account_ids = ["111111111111"]
}
