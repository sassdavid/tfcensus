# Retired: terragrunt skips this unit for every command.
include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "git::https://github.com/acme/infra-modules.git//legacy?ref=v0.9.0"
}

exclude {
  if      = true
  actions = ["all"]
}
