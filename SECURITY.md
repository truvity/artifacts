# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it privately via
[GitHub Security Advisories](https://github.com/truvity/artifacts/security/advisories/new).

Do NOT open a public issue for security vulnerabilities.

## Supported Versions

Only the latest release is supported with security updates.

| Version | Supported |
|---------|-----------|
| latest  | yes       |
| older   | no        |



- This module declares who may publish and who may read the registries, so a
  default is a security surface. It ships no default that widens access: every
  account, principal, permissions boundary and policy ARN is an input, and an
  input left empty opens nothing.
- A release role is trusted for exactly one OIDC subject: the repository and the
  release tag. The subject's head is stated by the caller, never composed, and a
  project that publishes from its own repository cannot inherit another's.
- It holds no credentials. AWS credentials belong to the Pulumi program that
  calls it; the pull-through cache's upstream credentials come from a reader the
  caller supplies and are passed on as Pulumi secrets.
- Tests run against Pulumi mocks and never touch an AWS account. The account IDs
  in tests are documentation placeholders, owned by no one.
