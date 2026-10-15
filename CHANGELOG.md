# Changelog

Entries are written for someone deciding whether to bump: what changed for
them, and for anything breaking, what to do.

## Unreleased

`pkg/registry`: `CodeArtifactReadStatements` and `ECRPreviewStatements` (new) are the IAM statements of the CodeArtifact read surface and of the preview-ECR surface (registry auth, pull through the caches, snapshot push); the account, region and push prefixes are parameters. `CheckReleaseSubjects` and `SubjectFetcher` (new) check every release role's OIDC subject against the `sub_claim_prefix` GitHub signs, asking once per repository, with the fetch left to the caller. `pkg/ecrcache`: `PullThroughPolicy` (new) is the consumer policy document for a pull-through prefix, next to the rules. Additive, nothing existing changes.

## v0.5.0

Security: `github.com/go-git/go-git/v6` v6.0.0-alpha.5 (indirect, through the Pulumi SDK; `go-billy/v6` follows to v6.0.0-alpha.2), which closes GHSA-hc8v-wwc9-vgxm (worktree operations may follow symlinks, high) and GHSA-qgq7-7hm3-q39j (malicious reference names may write outside the reference storage, moderate).

Security: the Go toolchain is now go1.27.2 and `golang.org/x/net` v0.60.0, which close govulncheck findings GO-2026-6610, -6611, -6612, -6613 and -6617 (`net/http` and HTTP/2 in the standard library and in x/net). No API change.

`pkg/registryspec` (new): the repository naming rules (`ChartRepositoryPrefix`, `ChartRepository`, `ProjectComponents`) in a package that imports the standard library only, so a config layer that only names a project's repositories no longer links Pulumi, `pulumi-aws` and the AWS SDK. `pkg/registry` re-exports all three unchanged; a test fails if `pkg/registryspec` ever gains a non-standard-library import. Additive, nothing existing changes.

## v0.4.0

`pkg/codeartifact` (new): `Deploy` declares a CodeArtifact domain and its repositories (upstreams in lookup order, at most one external connection each, the caller's KMS key or the AWS-managed one, optional tags). `Config.Adopt` imports a domain and repositories that already exist by their ARNs, so the first deploy imports them and changes nothing. The domain and repositories are protected unless `Config.Protect` points at false. Resource names: `codeartifact-<domain>` and `codeartifact-<domain>-<repository>`. Nothing existing changes.

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
