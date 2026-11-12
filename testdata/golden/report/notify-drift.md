## tfcensus drift

7 of 11 scope(s) have something to report.

### tfcensus: drift in live/dev/eu-west-1/app/s3

Drifted resources: 1.

| Level | Type | Target | Region | Rule | Detail |
|---|---|---|---|---|---|
| update | aws_s3_bucket | `aws_s3_bucket.assets` |  |  | tags.CostCenter |

### tfcensus: drift in live/dev/eu-west-1/network/sg

Drifted resources: 1.

| Level | Type | Target | Region | Rule | Detail |
|---|---|---|---|---|---|
| update | aws_security_group | `aws_security_group.web` |  |  | ingress[0].cidr_blocks[1] |

### tfcensus: drift in live/prod/eu-west-1/app/queue

Drifted resources: 1.

| Level | Type | Target | Region | Rule | Detail |
|---|---|---|---|---|---|
| delete | aws_sqs_queue | `aws_sqs_queue.jobs` |  |  |  |

### tfcensus: drift in live/prod/eu-west-1/app/s3

Drifted resources: 1.

| Level | Type | Target | Region | Rule | Detail |
|---|---|---|---|---|---|
| update | aws_s3_bucket | `aws_s3_bucket.assets` |  |  | tags.CostCenter |

### tfcensus: drift in live/prod/eu-west-1/data/cache

The plan failed, so drift is unknown: run error


### tfcensus: drift in live/prod/eu-west-1/data/db

Drifted resources: 1.

| Level | Type | Target | Region | Rule | Detail |
|---|---|---|---|---|---|
| update | aws_db_instance | `aws_db_instance.main` |  |  | instance_class, password |

### tfcensus: drift in live/staging/eu-west-1/app/s3

Drifted resources: 1.

| Level | Type | Target | Region | Rule | Detail |
|---|---|---|---|---|---|
| update | aws_s3_bucket | `aws_s3_bucket.assets` |  |  | tags.CostCenter |

