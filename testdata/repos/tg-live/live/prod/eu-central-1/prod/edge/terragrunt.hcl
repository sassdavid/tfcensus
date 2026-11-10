# The account lookup in locals needs AWS, but remote_state doesn't use it.
include "root" {
  path = find_in_parent_folders("root.hcl")
}

locals {
  caller_account = get_aws_account_id()
}

terraform {
  source = "git::https://github.com/acme/infra-modules.git//edge?ref=v1.4.0"
}
