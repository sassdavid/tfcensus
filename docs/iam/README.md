# IAM policy

[`tfcensus-readonly.json`](tfcensus-readonly.json) is the read-only IAM policy for every tfcensus
command except `move apply`, `move rollback` and `move mark-applied`. The
[IAM reference](https://tfcensus.sasskovacs.dev/reference/iam) explains each statement, how to
narrow it and what the move commands need.
