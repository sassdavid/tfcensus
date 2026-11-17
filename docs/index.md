---
layout: home
title: tfcensus
titleTemplate: Count what your Terraform manages
description: tfcensus lists every unit and state in a Terragrunt fleet and finds the AWS resources none of them manage. Read-only by default.
headline: Count what your Terraform manages.
lead: tfcensus lists every unit and state in a Terragrunt fleet and finds the AWS resources none of them manage. Read-only by default.
install: mise use github:sassdavid/tfcensus
actions:
  - text: Get started
    link: /getting-started
  - text: Install
    link: /install
    theme: alt
answers:
  - question: Which resources does no unit manage?
    command: tfcensus scan
    link: /cli/scan
  - question: Are the state buckets and lock tables safe?
    command: tfcensus audit backend
    link: /cli/audit/backend
  - question: What drifted, and in how many units?
    command: tfcensus drift
    link: /cli/drift
  - question: Which unit manages this ARN?
    command: tfcensus whois
    link: /cli/whois
  - question: How do I move resources between units?
    command: tfcensus move
    link: /guides/move
---

<HomeHero />

<AnswerRows />
