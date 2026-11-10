# The state key comes from a script, which only terragrunt render can evaluate.
include "root" {
  path   = find_in_parent_folders("root.hcl")
  expose = true
}

terraform {
  source = "git::https://github.com/acme/infra-modules.git//dns?ref=v1.4.0"
}

remote_state {
  backend = "s3"
  config = {
    bucket       = "acme-tfstate-${include.root.locals.account_name}"
    key          = "${run_cmd("--terragrunt-quiet", "echo", "prod/dns")}/terraform.tfstate"
    region       = "eu-west-1"
    use_lockfile = true
  }
}
