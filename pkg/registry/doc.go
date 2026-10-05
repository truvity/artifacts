// Package registry declares where a company's artifacts are kept in AWS: ECR
// repositories per project and tier, their lifecycle and repository policies,
// the release roles that CI assumes to publish (GitHub OIDC, pinned to a
// repository and a release tag), the warm BuildKit cache repository, the
// read-only private npm (CodeArtifact) reader roles, and the S3 bucket for
// Lambda archives.
//
// Two tiers (RepositoryKind): preview holds snapshot builds (mutable tags,
// bounded history); stable holds releases (immutable tags, append-only, and
// the only tier with publish identities). Which artifact kind belongs in
// which registry is the kind table in docs/kinds.md; this package is the
// mechanism for the private half of it.
//
// Everything here is a plain Pulumi function over caller-supplied inputs: no
// account, region, repository or role name is known to the module. Resource
// names are state identity and are documented in docs/contract.md; a change to
// one is a replace of a repository that holds images, so names change only
// with aliases.
package registry
