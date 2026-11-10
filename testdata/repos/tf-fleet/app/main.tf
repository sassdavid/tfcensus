terraform {
  backend "s3" {
    bucket       = "acme-tfstate-111111111111"
    key          = "app/terraform.tfstate"
    region       = "eu-west-1"
    use_lockfile = true

  }
}

provider "aws" {
  region              = "eu-west-1"
  allowed_account_ids = ["111111111111"]
}
