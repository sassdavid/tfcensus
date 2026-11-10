terraform {
  backend "s3" {
    bucket       = "acme-restricted-tfstate"
    key          = "restricted/terraform.tfstate"
    region       = "us-east-1"
    use_lockfile = true
    profile      = "acme-audit"
  }
}

provider "aws" {
  region              = "us-east-1"
  allowed_account_ids = ["444455556666"]
}
