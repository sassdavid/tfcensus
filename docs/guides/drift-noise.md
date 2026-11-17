---
description: Hide drift you have decided to live with, such as tags_all updates and computed hashes, with drift.noise presets and rules that carry a reason and an expiry date.
---

# Hide drift noise

Some drift comes back on every run and needs no action: the AWS provider copies `default_tags`
into `tags_all`, and a Lambda's `source_code_hash` changes with every build. `drift.noise` rules
hide such changes from the report and from `--fail-on drift`. A hidden change stays in the JSON
report with the rule that hid it, so nothing disappears without a trace.

## Turn on a preset

tfcensus has two built-in rules. List them in `.tfcensus.yaml`:

```yaml
drift:
  noise:
    - preset: aws-tags-all
    - preset: computed-hashes
```

`aws-tags-all` hides updates to `tags_all` on every `aws_*` resource. `computed-hashes` hides
updates to hashes and timestamps computed from content outside the configuration, such as
`source_code_hash`, `etag` and `output_base64sha256`, on every resource type. The
[drift noise presets reference](/reference/drift-noise) lists every attribute each one hides. A
preset entry takes no other key.

## Write a rule

A rule of your own needs a `name` and a `reason`, and at least one of `unit`, `type`, `address`,
`attribute` and `action` to say what it matches:

```yaml
drift:
  noise:
    - name: asg-desired-capacity
      type: aws_autoscaling_group
      attribute: desired_capacity
      action: update
      reason: the cluster autoscaler sets desired_capacity
    - name: legacy-sandbox
      unit: "live/sandbox/*"
      reason: the sandbox account is rebuilt every week
      expires: "2027-03-31"
```

| Field | Matches |
|---|---|
| `unit` | the unit path, as a glob where `*` matches any characters, `/` included |
| `type` | the resource type, as a glob such as `aws_s3_*` |
| `address` | the resource address, as a glob such as `module.tags.*` |
| `attribute` | an attribute path such as `tags_all` or `ingress[*].cidr_blocks`, and everything below it |
| `action` | the drift action: `create`, `update`, `delete` or `replace` |

Every field you set must match. With `attribute`, the rule hides only that attribute's changes,
and the change stays in the report while any other attribute of it drifted. Once every changed
attribute is hidden, the whole change is hidden. Without `attribute`, the rule hides the whole
change.

Rules are tried in order and the first match wins. A hidden change carries the rule ID
`user/<name>` (or `preset/<name>`) and the reason. A name may appear only once, and tfcensus checks
every rule when it loads the config, so a typo stops the run with exit 2 before any plan.

## Give a rule an end date

`expires` is the last day a rule applies, as a quoted `"YYYY-MM-DD"` date in UTC. After it the
rule stops hiding drift, and drift warns:

```text
warning: noise rule "legacy-sandbox" is past its expires date (2027-03-31) and no longer hides drift: 4 change(s) reported again
```

Use it for drift you plan to fix, so the rule doesn't outlive the reason.

## Actions no rule can hide

`drift.never_hide` lists the actions that no rule hides. The default is `delete` and `replace`,
so a resource deleted outside terraform always shows, whatever the rules say. When a rule matches
such a change, the change stays visible with a note that says which rule didn't apply. In
`--mode full`, the action the plan would take to revert the drift counts too. Remove an action from
the list only if you really want rules to hide it:

```yaml
drift:
  never_hide: [delete, replace]
```

## Where hidden drift shows

- The table has a `HIDDEN` column per unit, and the summary line ends with the count hidden by
  noise rules.
- Hidden changes don't count for `--fail-on drift` or `--fail-on drift:reverts`, and a unit whose
  every change is hidden counts as clean.
- In JSON, a hidden change has a `hidden` object with `rule` and `reason`, and a hidden attribute
  has a `noise` object with the same fields. The [drift schema](/reference/schemas) describes them.
- In SARIF, a hidden change is a suppressed result whose justification is the rule ID and reason.

To check what a rule hides, run drift with `--format json` and look for the rule ID:

```sh
tfcensus drift --format json | jq '.data.changes[] | select(.hidden.rule == "user/asg-desired-capacity") | .address'
```
