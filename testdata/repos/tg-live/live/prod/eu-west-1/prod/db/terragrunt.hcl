# The database keeps its DynamoDB lock table while it moves to S3 lockfiles.
include "root" {
  path   = find_in_parent_folders("root.hcl")
  expose = true
}

terraform {
  source = "git::https://github.com/acme/infra-modules.git//rds?ref=v1.4.0"
}

dependency "vpc" {
  config_path = "../vpc"

  mock_outputs = {
    vpc_id = "vpc-00000000"
  }
}

remote_state {
  backend = "s3"
  config = {
    bucket         = "acme-tfstate-${include.root.locals.account_name}"
    key            = "${path_relative_to_include("root")}/terraform.tfstate"
    region         = "eu-west-1"
    encrypt        = true
    use_lockfile   = true
    dynamodb_table = "acme-tflock"
  }
}

inputs = {
  vpc_id = dependency.vpc.outputs.vpc_id
}
