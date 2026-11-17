---
layout: home
title: tfcensus
titleTemplate: Count what your Terraform manages
description: tfcensus lists every unit and state in a Terragrunt fleet and finds the AWS resources none of them manage. Read-only by default.
headline: Count what your Terraform manages.
lead: tfcensus lists every unit and state in a Terragrunt fleet and finds the AWS resources none of them manage. Read-only by default.
install: mise use github:sassdavid/tfcensus
actions:
  - text: Install
    link: /install
  - text: Read-only IAM policy
    link: /iam/
    theme: alt
answers:
  - question: Which resources does no unit manage?
    command: tfcensus scan
    link: https://github.com/sassdavid/tfcensus/blob/v0.4.0/docs/cli/tfcensus_scan.md
  - question: Are the state buckets and lock tables safe?
    command: tfcensus audit backend
    link: https://github.com/sassdavid/tfcensus/blob/v0.4.0/docs/cli/tfcensus_audit_backend.md
  - question: What drifted, and in how many units?
    command: tfcensus drift
    link: https://github.com/sassdavid/tfcensus/blob/v0.4.0/docs/cli/tfcensus_drift.md
  - question: Which unit manages this ARN?
    command: tfcensus whois
    link: https://github.com/sassdavid/tfcensus/blob/v0.4.0/docs/cli/tfcensus_whois.md
  - question: How do I move resources between units?
    command: tfcensus move
    link: /runbooks/move
---

<HomeHero />

<AnswerRows />
