# Contributing

Thanks for looking. This repository holds Pulumi functions that declare where a
company's artifacts live, and the rules (docs/kinds.md) for which kind goes
where.

## Before you open a pull request

```sh
devbox shell
just check      # build, test, lint, leak-canary
```

`just check` is what CI runs on a pull request. `just vuln` runs on a schedule.

## What belongs here

Mechanism. A function takes the account, the region, the projects and the ARNs
it needs as inputs; it does not know any particular estate. If a value would be
different in another organisation, it is an input. `just leak-canary` fails on a
twelve-digit account ID or an internal hostname outside the tests.

## Resource names are API

A Pulumi resource name is the resource's identity in state (docs/contract.md).
Do not rename one. If a change cannot avoid it, add an alias in the same change
and say so in the CHANGELOG.

## Changelog

Every user-visible change adds a line under `## Unreleased`.
