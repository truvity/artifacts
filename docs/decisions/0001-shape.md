# 0001. Plain functions, one module, mechanism only

Status: accepted, 2026-10-06.

## Context

The registry code lived in one company's deployment repository, wired to that
company's configuration loader, account topology and naming. The same mechanism
(ECR repositories per project and tier, lifecycle, repository policies, release
roles, a pull-through cache) is needed wherever artifacts are published.

## Decision

- **A Go module of Pulumi functions**, not components. The resources are
  registered directly under the caller's stack, so a stack that already holds
  them adopts the module with an empty preview: the same URNs, no aliases. A
  component would add a parent and need an alias on every child.
- **Mechanism only.** Account IDs, regions, project lists, repository names and
  policy ARNs are inputs. The module does not load a file: the caller builds
  `registry.Config` (it carries `yaml` tags, so a YAML file can be decoded
  straight into it). A leak canary in CI fails on a twelve-digit account ID
  outside the tests.
- **Public.** Nothing in it is an estate fact, and the kind table
  (docs/kinds.md) is the rule other repositories are held to.
- **Names are API** (docs/contract.md).

## Not here

A mirror for clusters off AWS. (CodeArtifact domains and repositories were
listed here until `pkg/codeartifact` brought them in.)
