defaults {
  schema_cache_directory     = "../service/cloudformation/schemas"
  terraform_type_name_prefix = "awscc"
}

resource_schema "aws_ec2_vpc" {
  cloudformation_type_name = "AWS::EC2::VPC"
}

resource_schema "aws_iam_policy" {
  cloudformation_type_name               = "AWS::IAM::Policy"
  suppress_plural_data_source_generation = true
}
