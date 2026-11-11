terraform {
  backend "s3" {
    bucket = "acme-audit-good"
    key    = "network/terraform.tfstate"
    region = "eu-west-1"
    use_lockfile = true
  }
}

provider "aws" {
  region              = "eu-west-1"
  allowed_account_ids = ["111111111111"]
}
