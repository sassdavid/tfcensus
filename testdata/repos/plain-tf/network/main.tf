terraform {
  required_version = ">= 1.5.0"

  backend "s3" {
    bucket         = "acme-tfstate-444455556666"
    key            = "network/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "acme-tflock"
  }
}

provider "aws" {
  region = "us-east-1"
}

module "vpc" {
  source = "../modules/vpc"
}
