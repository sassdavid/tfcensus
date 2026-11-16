| resources | unmanaged | fp | tp | unsure | not reviewed | fp rate | undetermined |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 8 | 5 | 2 | 1 | 1 | 1 | 40.0% | 1 |

| category | count |
| --- | --- |
| unmanaged | 5 |
| aws-managed | 1 |
| managed | 1 |
| undetermined | 1 |

| unmanaged type | fp | tp | unsure | not reviewed |
| --- | --- | --- | --- | --- |
| AWS::EC2::NetworkInterface | 1 | 0 | 0 | 0 |
| AWS::ECR::Repository | 0 | 0 | 1 | 0 |
| Native::IAM::RolePolicyAttachment | 1 | 1 | 0 | 0 |
| ecs:task | 0 | 0 | 0 | 1 |

| false-positive group | count |
| --- | --- |
| service ENIs | 1 |
| service-role children | 1 |
