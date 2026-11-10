# The bucket name comes from a command that fails, so neither the fast path nor render can resolve it.
include "root" {
  path   = find_in_parent_folders("root.hcl")
  expose = true
}

terraform {
  source = "git::https://github.com/acme/infra-modules.git//secrets?ref=v1.4.0"
}

remote_state {
  backend = "s3"
  config = {
    bucket       = run_cmd("--terragrunt-quiet", "false")
    key          = "${path_relative_to_include("root")}/terraform.tfstate"
    region       = "eu-west-1"
    use_lockfile = true
  }
}
