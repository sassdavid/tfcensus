terraform {
  required_version = ">= 1.10.0"

  backend "s3" {
    bucket               = "acme-app-state"
    key                  = "app/terraform.tfstate"
    region               = "eu-west-1"
    workspace_key_prefix = "workspaces"
    use_lockfile         = true

    assume_role = {
      role_arn = "arn:aws:iam::555555555555:role/tfstate"
    }
  }
}
