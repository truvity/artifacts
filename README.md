# artifacts

**Where every artifact is published and consumed, as code.**

The mechanism for the private half of the answer (ECR registries, lifecycle,
repository policies, release roles, the pull-through cache) as Pulumi functions,
and the rule for the whole of it (which artifact kind goes to which registry,
public or private, who may write, how it is named and versioned).

| What | Where |
| --- | --- |
| Go module `github.com/truvity/artifacts` | `go get github.com/truvity/artifacts@v0.1.0` |
| `pkg/registry` — ECR repositories per project and tier, lifecycle, repository policies, release roles (GitHub OIDC), the warm BuildKit cache, read-only CodeArtifact reader roles, the Lambda archive bucket | Go package, Pulumi functions |
| `pkg/ecrcache` — the standard ECR pull-through cache rules, credentials injected | Go package, over `github.com/truvity/k8s` |
| The kind table, naming and versioning | [docs/kinds.md](docs/kinds.md) |
| What the module promises (inputs, resource names, tiers, release identity) | [docs/contract.md](docs/contract.md) |

## The rule in one table

| Kind | Public | Private |
| --- | --- | --- |
| Image | GHCR | ECR |
| Chart | GHCR (OCI) | ECR |
| npm, Maven, Python | npmjs, Maven Central, PyPI (trusted publishing) | CodeArtifact |
| Go module | the git tag | the git tag |
| Lambda zip, flake, Pkl, binary | Release asset | S3 |

A cluster on AWS pulls through the account's ECR pull-through cache. The full
table, with paths and writers, is [docs/kinds.md](docs/kinds.md).

## Who it is for

A team that publishes artifacts from GitHub Actions and keeps private ones in
AWS, and wants the registries reviewed as code with one rule for where a thing
goes. It assumes you have a Pulumi backend and credentials for the registry
accounts. It chooses none of your account IDs, regions, projects or names.

## The model

Three ideas.

**One rule per kind.** Visibility picks the column: public artifacts publish to
public registries with the workflow's own token, private ones to the private
registry with a role scoped to the repository and its release tag. The rule is a
table, and a new artifact gets the row of its kind ([docs/kinds.md](docs/kinds.md)).

**Plain functions over inputs.** `pkg/registry` and `pkg/ecrcache` are Pulumi
functions. They register resources directly under the caller's stack, so a stack
that already declares them adopts the module with an empty preview. Account IDs,
regions, projects and ARNs are the caller's.

**Names are API.** A Pulumi resource name is the resource's identity in state, so
the names are a contract ([docs/contract.md](docs/contract.md)), and a change to
one ships with an alias.

## Install and a worked example

```sh
go get github.com/truvity/artifacts@v0.1.0
```

```go
import (
    "github.com/truvity/artifacts/pkg/ecrcache"
    "github.com/truvity/artifacts/pkg/registry"
)

// In the registry account's program, with a provider for the region:
err := registry.DeployECR(ctx, logger, registry.DeployECRConfig{
    Project:        &registry.ProjectConfig{Name: "shop", ECR: []string{"api", "charts/api"}},
    Region:         "eu-central-1",
    Kind:           registry.RepositoryKindStable,
    AWSProvider:    provider,
    AccountID:      accountID,
    ReaderAccounts: readers,
})

// One release role per project, trusted for its repository on its release tag:
err = registry.DeployRelease(ctx, logger, registry.ReleaseStackConfig{ /* … */ })

// Or a stack per project: the shared part once, each project's role in its own stack.
_, err = registry.DeployReleaseShared(ctx, logger, cfg) // in the shared stack
err = registry.DeployReleaseRoles(ctx, logger, cfg,
	pulumi.String(registry.GitHubOIDCProviderARN(accountID)).ToStringOutput(), "shop")

// In a cluster account: pull-through cache rules.
err = ecrcache.Deploy(ctx, logger, ecrcache.Options{Provider: provider, Credentials: read})
```

The functions register resources directly under your stack, so adopting a stack
that already declares them is an empty preview ([docs/contract.md](docs/contract.md)).

## Consumers

None recorded at v0. A repository that adopts it adds a line here.

## Neighbours

- [k8s](https://github.com/truvity/k8s) — `pkg/aws/pullthroughcache`, the
  component `pkg/ecrcache` configures, and the URN-stability model this
  repository follows.
- [aws-structure](https://github.com/truvity/aws-structure) — the accounts these
  registries live in.
- [policy](https://github.com/truvity/policy) — the contracts this repository is
  held to (`docs/contracts/component.md`).
- [ci-workflows](https://github.com/truvity/ci-workflows) — the shared CI and
  release workflows, including the public and private release paths the kind
  table describes.

## Documentation

- [docs/kinds.md](docs/kinds.md) — which artifact kind goes where, who may write,
  naming and versioning.
- [docs/contract.md](docs/contract.md) — inputs, resource names, tiers, release
  identity.
- [docs/decisions/](docs/decisions/) — the decisions, one page each.
- [SECURITY.md](SECURITY.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## The rule that makes this repository public

Mechanism only. Account IDs, regions, project and repository names, role and
policy ARNs and credentials are the caller's, in the caller's private
configuration. Nothing here defaults to one estate's value, and the tests use
documentation placeholders and never reach an AWS account.
[`hack/leak-canary.sh`](hack/leak-canary.sh) enforces the mechanical half over
tracked files and runs in the gate; its only exceptions are the ARN grammar under
`pkg/` and the placeholder IDs in tests, by path, with the reason in the script.

## Status

v0: the API may still move in a minor release, and every move is a `Breaking:`
bullet in the [CHANGELOG](CHANGELOG.md). Not here yet: CodeArtifact domains and
repositories as code (the reader roles are), and a registry mirror for clusters
off AWS.

## Development

```sh
devbox shell        # pins every tool
just check          # build, test, lint, leak-canary
just vuln           # reachable Go advisories (its own workflow in CI, not in check)
```

## Releasing

Releases are a `v*` tag, which the shared release workflow turns into the Go
module version and a GitHub Release. Automatic patch releases are not armed; the
first release and every minor and major are hand-cut tags.

## Licence

MIT. See [LICENSE](LICENSE).
