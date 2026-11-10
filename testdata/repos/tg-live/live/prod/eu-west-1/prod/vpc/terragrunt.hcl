include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "git::https://github.com/acme/infra-modules.git//vpc?ref=v1.4.0"
}
