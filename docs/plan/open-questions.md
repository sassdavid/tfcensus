# tfcensus: open questions

| # | Question | Default if unanswered | Needed by |
|---|---|---|---|
| ~~Q1~~ | **Resolved 2026-11-09:** name `tfcensus`; GitHub owner `sassdavid` (personal) | — | — |
| Q2 | **Kept open on purpose (spike in M1).** In plain terms: to read a unit's state, tfcensus must know *where* it is stored (bucket + key). Terragrunt computes that from `remote_state` in `root.hcl`, often via functions like `path_relative_to_include()`. Question: can Terragrunt's own `render --json` hand us the computed bucket/key (easy, exact), or must tfcensus evaluate the common patterns itself (fast, but approximate)? Answered by running both against a fixture repo in M1 | fast-path evaluator, render as cross-check | M1 |
| Q3 | Where does the Control Tower Config aggregator live in the author's org (Audit account name/profile), and can we read it with a read-only role? | per-account Config queries | M5 |
| Q4 | Treat CloudFormation as a second IaC source (read stack resources → `managed-by-cfn` with stack name) instead of only tag-based `other-iac`? | tags only in v0.1; CFN source in v0.3 | M6 |
| Q5 | Pulumi / CDKTF state as IaC sources? | no (non-goal before 1.0) | — |
| Q6 | Ship a container image with terragrunt+terraform bundled, or BYO binaries only? | BYO (licensing: terraform BSL redistribution) | M6 |
| Q7 | Public validation data: how much of the author's fleet results can appear in docs? | aggregated numbers only, no IDs | M4 |
| Q8 | **Deferred:** no sandbox AWS account exists yet. Development and CI must work with **no AWS account** (see [testing.md](testing.md)). A real-account validation phase is optional and added later | no account; emulator + recorded fixtures | before v0.1 release (optional) |
| Q9 | Bitbucket Cloud notifier priority vs GitHub (the author's SCM is Bitbucket) | both in v0.5, Bitbucket first | M10 |
