include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "git::https://github.com/acme/infra-modules.git//api?ref=v2.0.0"
}

inputs = {
  name = values.name
}
