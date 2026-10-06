# Changelog

Entries are written for someone deciding whether to bump: what changed for
them, and for anything breaking, what to do.

## v0.3.0

`pkg/registry`: `DeployRelease` splits into `DeployReleaseShared` (the GitHub
OIDC provider, the warm BuildKit cache, the CodeArtifact reader roles; returns
the provider's ARN) and `DeployReleaseRoles` (the release role of the named
projects, or of all), so a deployment can keep each project's release role in a
stack of the project's own. `GitHubOIDCProviderARN` names the provider when it
lives in another stack. `DeployRelease` is unchanged: the same resources with
the same names. Not breaking.

## v0.2.0

`pkg/registry`: `ChartRepository` and `ProjectComponents` (new) name the repositories of a project: its Helm charts under `charts/` first, then its images. A deployment no longer restates the `charts/<chart>` rule. No existing name changes.

## v0.1.0

First release. Moved from a company deployment repository with no change to a
single Pulumi resource name or property: the deployment's URN and property
goldens are byte-identical before and after.

- `pkg/registry`: ECR repositories per project and tier (`DeployECR`),
  lifecycle (`ECRLifecyclePolicy`; preview keeps the last 750 images, stable is
  append-only), repository policies with cross-account readers, the Lambda
  archive bucket (`DeployS3`), the GitHub OIDC provider and one release role per
  project (`DeployRelease`), the subject derivation and its drift check
  (`ReleaseTargets`, `CompareSubjects`), the warm BuildKit cache repository and
  the read-only CodeArtifact reader roles (`DeployCodeArtifactReaderRoles`).
  The read-policy and permissions-boundary ARNs are inputs.
- `pkg/ecrcache`: the standard pull-through cache rules (Docker Hub, GHCR,
  Quay, registry.k8s.io) over `truvity/k8s`'s `pullthroughcache`, with the
  credential source injected.
- `docs/kinds.md`: which artifact kind goes where, public or private, who may
  write, naming and versioning rules. `docs/contract.md`: the resource-name
  contract.
