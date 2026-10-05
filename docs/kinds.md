# Where each kind of artifact goes

One table, one rule per column. A new artifact does not get a new place; it
gets the row of its kind.

The rule that picks the column is **visibility**: a public repository publishes
to public registries with no credential beyond the workflow's own token; a
private repository publishes to the private registry with a role scoped to the
repository and the release tag.

| Kind | Public | Private |
| --- | --- | --- |
| Container image | `ghcr.io/<owner>/<repo>/<image>` | ECR, stable tier: `<project>/<image>` (snapshots in the preview tier) |
| Helm chart (OCI) | `oci://ghcr.io/<owner>/charts/<chart>` | ECR: `<project>/charts/<chart>` |
| npm package | npmjs.org through trusted publishing (OIDC, no token) | CodeArtifact `npm-private` |
| Maven artifact | Maven Central | CodeArtifact maven repository |
| Python package | PyPI through trusted publishing; the wheel is also a Release asset, for pinning by URL | CodeArtifact pypi repository |
| Go module | the git tag, resolved by the module proxy; a nested module takes a `<dir>/vX` tag | the git tag, with the private-module settings on the consumer |
| Lambda archive, Nix flake, Pkl package | Release asset, named `<id>_<version>_<os>_<arch>.zip` with a `checksums.txt` | S3 bucket `artifacts-<tier>-<region>` in the registry account |
| CLI binary | Release asset, and a Nix flake | the same, from the private repository |
| Source map | image in GHCR | image in ECR |
| BuildKit warm cache | n/a | ECR `ci/buildkit-cache` (untagged layers expire in 7 days) |

GitHub Packages is not in the table on purpose: it refuses anonymous installs
even for public packages, so every consumer of a "public" package would need a
token. Public means anonymously readable.

## How a cluster pulls

- **On AWS** every cluster pulls through the account's ECR pull-through cache
  (`pkg/ecrcache`): `docker-hub/`, `github/` (ghcr.io), `quay/` and
  `registry-k8s-io/`. The first pull lands the image in a local repository; later
  pulls are same-region and free of upstream rate limits. Private images are read
  from the registry accounts through a cross-account repository policy
  (`pkg/registry`), never copied.
- **Off AWS** a mirror in front of the upstreams is the equivalent. It is not part
  of this module yet.

## Who may write

| Where | Writer |
| --- | --- |
| GHCR, Release assets, Maven Central, npmjs, PyPI | the repository's own release workflow, with its `GITHUB_TOKEN` or trusted publishing. Never a personal token. |
| ECR stable | the `release-<project>` role: assumable only by the project's repository, on its release tag (`ReleaseTargets`), plus an optional human break-glass permission set |
| ECR preview | the CI pool's identity, or a person's own SSO profile. There are no publish roles for this tier |
| CodeArtifact | the same shape as ECR stable; reading is a separate read-only role that cannot push |

## Naming

- One path shape everywhere: `<project>/<artifact>`. In GHCR the project is the
  repository, so `<owner>/<repo>/<image>`; charts live beside, under `charts/`.
- The repository path is the identity. The ECR repository `<project>/<image>` is
  created from the project's artifact list; adding an image is one line in that
  list, and no policy changes (access is the prefix `<project>/*`).
- Resource names in Pulumi follow `docs/contract.md` and do not change.

## Versioning

- A chart and the images it ships carry **one** version: the git tag, stamped at
  release and never committed. Charts pin images by digest.
- A repository that holds one product is tagged `v<semver>`. A repository that
  holds several (a monorepo) tags `<project>/v<semver>`; a Go sub-module tags
  `<dir>/v<semver>`.
- Images are always multi-architecture.
- A pre-release is `v<semver>-rc.<n>`. A snapshot is `<version>-<sha8>-<channel>`
  and exists in the preview tier only.
- **Stable is append-only.** Tags are immutable (the only mutable ones are
  `latest*`), nothing is expired, a released artifact is never deleted. A rollback
  target cannot vanish.
- **Preview is bounded.** It keeps the last N images by count, regardless of tag
  (the default is 750, roughly 150 multi-architecture builds). A retention rule keyed
  to a tag pattern fails silently the day the naming habit changes; counting
  every image has no such failure.
