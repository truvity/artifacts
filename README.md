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

## Using it

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

// In a cluster account: pull-through cache rules.
err = ecrcache.Deploy(ctx, logger, ecrcache.Options{Provider: provider, Credentials: read})
```

The functions register resources directly under your stack, so adopting a stack
that already declares them is an empty preview ([docs/contract.md](docs/contract.md)).

## Status

v0.1.0. Not here yet: CodeArtifact domains and repositories as code (the reader
roles are), and a registry mirror for clusters off AWS. See the
[CHANGELOG](CHANGELOG.md).
