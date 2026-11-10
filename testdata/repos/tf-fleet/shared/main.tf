terraform {
  backend "s3" {
    bucket       = "acme-shared-tfstate"
    key          = "shared/terraform.tfstate"
    region       = "eu-central-1"
    use_lockfile = true
    assume_role = {
      role_arn = "arn:aws:iam::333333333333:role/tfstate-reader"
    }
  }
}

provider "aws" {
  region              = "eu-central-1"
  allowed_account_ids = ["333333333333"]
}
