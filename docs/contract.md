# Contract

What this module promises its callers. Anything not written here is not a
promise.

## Inputs, not defaults

The module knows no account, region, project, repository or role name. Every
one arrives from the caller: an account ID, the provider for a region, the list
of projects and their repositories, the permissions-boundary and read-policy
ARNs. An input left empty opens nothing. There is no default that widens access.

## Resource names are API

A Pulumi resource name is part of the URN, so it is the identity of the
resource in a stack's state. Renaming one is a replace; for a repository that
holds images, a delete. The names below are therefore a contract; a change to
one ships with an alias in the same release.

| Function | Resource | Name |
| --- | --- | --- |
| `DeployECR` | `aws:ecr/repository:Repository` | `<project>/<component>/<region>` |
| | `aws:ecr/lifecyclePolicy:LifecyclePolicy` | `<project>/<component>/lifecycle/<region>` (preview tier only) |
| | `aws:ecr/repositoryPolicy:RepositoryPolicy` | `<project>/<component>/policy/<region>` |
| | export | `repository_<name with / and - as _>_url` |
| `DeployS3` | `aws:s3/bucket:Bucket` | `artifacts-<tier>-<region>`, with `-lifecycle`, `-policy`, `-public-access`, `-encryption`, `-versioning` beside it |
| `DeployRelease` = `DeployReleaseShared` + `DeployReleaseRoles` | `aws:iam/openIdConnectProvider:OpenIdConnectProvider` (shared) | the caller's `OIDCProviderResourceName` |
| | `aws:iam/role:Role` (roles) | `release-role-<project>` (the role is named `release-<project>`) |
| | `aws:iam/rolePolicy:RolePolicy` | `release-policy-<project>` |
| `DeployCIBuildkitCache` | repository and lifecycle | `ci-buildkit-cache`, `ci-buildkit-cache-lifecycle` |
| `DeployCodeArtifactReaderRoles` | role and attachment | `codeartifact-reader-<project>`, `codeartifact-reader-attach-<project>` (the role is named `release-<project>-codeartifact`) |
| `ecrcache.Deploy` | component | `ecr-cache`, with the children `ptc-rule-<prefix>`, `ptc-secret-<prefix>`, `ptc-secret-version-<prefix>` |

The functions register their resources directly under the caller's stack, not
under a component: adopting a stack that already had them keeps every URN
without an alias.

## Tiers

- `RepositoryKindStable`: tags are `IMMUTABLE_WITH_EXCLUSION` (`latest*` stays
  mutable), no lifecycle policy, publish roles exist, the BuildKit warm cache is
  deployed. Readers are every account in `ReaderAccountIDs`.
- `RepositoryKindPreview`: tags are mutable, the lifecycle keeps the last 750
  images, no publish roles exist. Readers are the kernel, sandbox and devel
  account roles.

## Release identity

A project's role is trusted for exactly one subject: the OIDC subject GitHub
signs for the project's repository on its release tag.

- The subject's head is **stated**, never composed from the owner and the name.
  GitHub signs older repositories by name and newer ones by immutable ID, and
  composing the wrong shape yields a role no token ever matches, found at tag
  time. An unstated head is an error that carries the command that answers it
  (`gh api /repos/<owner>/<repo>/actions/oidc/customization/sub --jq .sub_claim_prefix`).
- A project that publishes from its own repository must state its own head; it
  can never inherit the shared repository's.
- `CompareSubjects` checks the stated heads against the live ones; run it in
  review, not at tag time.
- The read-only CodeArtifact role trusts the same subject, plus optionally extra
  tag families and named GitHub environments. It widens read, never write.

## Credentials

The module holds none. `ecrcache.Deploy` asks the caller's `CredentialReader`
for a field by name, at program time, and passes the value to Secrets Manager
as a Pulumi secret.

## Stability

v0.x: a minor release may change a signature, always with a CHANGELOG entry
that says what to do. Resource names do not change within a major version.
