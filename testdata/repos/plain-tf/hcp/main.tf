terraform {
  cloud {
    organization = "acme"

    workspaces {
      name = "hcp-app"
    }
  }
}

provider "aws" {
  region = "eu-central-1"
}
