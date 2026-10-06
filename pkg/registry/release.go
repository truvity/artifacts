package registry

import (
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	// ReleaseStackConfig is everything DeployRelease needs from the caller.
	ReleaseStackConfig struct {
		// Kind is the tier of the account. Project publish identities and
		// the warm BuildKit cache exist for the stable tier only.
		Kind RepositoryKind

		// Config declares the projects, their release sources and the
		// optional human break-glass permission set.
		Config *Config

		// AccountID is the registry account the roles live in.
		AccountID string

		// AWSProvider is the provider of the registry region.
		AWSProvider *aws.Provider

		// OIDCProviderResourceName names the GitHub OIDC provider resource
		// (one per account). Kept an input so an account that already holds
		// the provider under a name keeps it.
		OIDCProviderResourceName string

		// PermissionsBoundaryARN caps every release role: a publish identity
		// must never write IAM. Empty means no boundary.
		PermissionsBoundaryARN string

		// BuildkitCache additionally deploys the warm BuildKit cache
		// repository in a non-stable account (the stable tier always has it).
		BuildkitCache bool

		// CodeArtifactReaders, when set, deploys the read-only private npm
		// reader roles (Config.Release.CodeArtifactReaders) in this account.
		CodeArtifactReaders *CodeArtifactReaderOptions
	}
)

// DeployRelease deploys a registry account's publish identities: the GitHub
// OIDC provider (once) and one release-{project} role per project that has
// ECR repositories.
//
// A role for project X is assumable only by workflows of the project's
// source repository running on its release tag (see ReleaseTargets) — so a
// PR build, or a workflow of any other project, cannot obtain a release
// identity. Repository access is the ARN prefix {project}/*, which means a
// project adding an image needs no policy change.
//
// It is DeployReleaseShared followed by DeployReleaseRoles for every
// project. A deployment that keeps each project's role in a stack of the
// project's own runs the two separately; the resource names are the same.
func DeployRelease(c *pulumi.Context, logger *slog.Logger, cfg ReleaseStackConfig) error {
	oidcProviderARN, err := DeployReleaseShared(c, logger, cfg)
	if err != nil {
		return err
	}

	return DeployReleaseRoles(c, logger, cfg, oidcProviderARN)
}

// DeployReleaseShared deploys what a registry account's publish identities
// share: the GitHub OIDC provider, the warm BuildKit cache (always on the
// stable tier, with BuildkitCache elsewhere) and, with CodeArtifactReaders,
// the private npm reader roles. It returns the OIDC provider's ARN, which
// DeployReleaseRoles trusts.
func DeployReleaseShared(c *pulumi.Context, logger *slog.Logger, cfg ReleaseStackConfig) (pulumi.StringOutput, error) {
	config := cfg.Config

	if config.Release == nil || config.Release.SourceRepo == "" {
		return pulumi.StringOutput{}, fmt.Errorf("registry config has no release.source_repo — the release stack has nothing to trust")
	}

	provider := cfg.AWSProvider

	oidcProvider, err := EnsureGitHubOIDCProvider(c, logger, provider, cfg.OIDCProviderResourceName)
	if err != nil {
		return pulumi.StringOutput{}, err
	}

	// The stable tier's BuildKit warm cache: the same ci/buildkit-cache the
	// preview tier keeps, here so a release on stable runners exports its
	// layers with `--cache-to type=registry` and the next release starts
	// warm even after a builder's volume is gone. Every release role may
	// push to it — it holds layer blobs and cache manifests under mutable
	// per-image tags, nothing a consumer ever pulls by name, so the grant
	// widens no publish boundary; the lifecycle expires untagged layers in
	// days.
	if cfg.Kind == RepositoryKindStable {
		if err := DeployCIBuildkitCache(c, logger, provider); err != nil {
			return pulumi.StringOutput{}, err
		}
	}

	// The BuildKit warm-cache repo outside the stable tier has its OWN gate,
	// deliberately not nested inside anything else: it was nested once, and
	// retiring the thing it was nested in silently un-declared it, so the
	// next plan read "1 to delete" for the repository snapshot builds still
	// export into (and cache-to failures are silent client-side).
	if cfg.BuildkitCache && cfg.Kind != RepositoryKindStable {
		if err := DeployCIBuildkitCache(c, logger, provider); err != nil {
			return pulumi.StringOutput{}, err
		}
	}

	// Private npm read for hosted-runner releases. Lives with the account's
	// GitHub OIDC provider, which is what the reader roles trust.
	if cfg.CodeArtifactReaders != nil {
		if err := DeployCodeArtifactReaderRoles(c, logger, provider,
			oidcProvider.Arn, cfg.AccountID, config, *cfg.CodeArtifactReaders); err != nil {
			return pulumi.StringOutput{}, err
		}
	}

	return oidcProvider.Arn, nil
}

// GitHubOIDCProviderARN is the ARN of an account's GitHub Actions OIDC
// provider (IAM names it by its issuer host). DeployReleaseRoles takes it
// when the provider is declared in another stack.
func GitHubOIDCProviderARN(accountID string) string {
	return fmt.Sprintf("arn:aws:iam::%s:oidc-provider/%s", accountID, githubOIDCProviderHost)
}

// DeployReleaseRoles deploys the release-{project} role of each named
// project, or of every project with ECR repositories when none is named,
// trusting the account's GitHub OIDC provider oidcProviderARN. Naming a
// project that has no repositories is an error.
//
// STABLE ONLY. Project publish identities exist for the stable tier,
// whose trust is pinned to the project's release tags. The preview tier
// deliberately has NONE: snapshot pushes ride the CI pool's identity,
// and people publish with their own SSO profile. A preview role would
// carry an any-ref trust, admitting any branch or PR workflow of the
// source repo to push to the preview registry; an unused credential
// path is a liability, not a convenience.
func DeployReleaseRoles(
	c *pulumi.Context,
	logger *slog.Logger,
	cfg ReleaseStackConfig,
	oidcProviderARN pulumi.StringOutput,
	only ...string,
) error {
	ctx := c.Context()
	config := cfg.Config

	if config.Release == nil || config.Release.SourceRepo == "" {
		return fmt.Errorf("registry config has no release.source_repo — the release stack has nothing to trust")
	}

	provider := cfg.AWSProvider
	accountID := cfg.AccountID

	var humanPatterns []string
	if ps := config.Release.HumanSSOPermissionSet; ps != "" {
		humanPatterns = []string{fmt.Sprintf(
			"arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/*/AWSReservedSSO_%s_*",
			accountID, ps,
		)}
	}

	projects := make([]ProjectConfig, 0, len(config.Projects))

	if cfg.Kind == RepositoryKindStable {
		for _, p := range config.Projects {
			if len(p.RepositoryNames()) > 0 && (len(only) == 0 || slices.Contains(only, p.Name)) {
				projects = append(projects, p)
			}
		}
	}

	for _, name := range only {
		if !slices.ContainsFunc(projects, func(p ProjectConfig) bool { return p.Name == name }) {
			return fmt.Errorf("release role for %q: no %s project with ECR repositories by that name", name, cfg.Kind)
		}
	}

	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })

	// Every stable release role may push to the stable BuildKit cache
	// (DeployReleaseShared).
	var stableCacheARN string
	if cfg.Kind == RepositoryKindStable {
		stableCacheARN = fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", config.PrimaryRegion, accountID, CIBuildkitCacheRepo)
	}

	for _, p := range projects {
		// Where this project publishes FROM: the shared repository with the
		// {project}/v* tag, or the project's own (Release.Overrides).
		target, err := releaseSubject(config.Release, p.Name)
		if err != nil {
			return err
		}

		_, err = DeployReleaseRole(c, logger, provider, oidcProviderARN, ReleaseRoleConfig{
			Project:             p.Name,
			SubjectPatterns:     []string{target.Subject},
			PermissionsBoundary: cfg.PermissionsBoundaryARN,
			RepositoryARNs: []string{
				fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s/*", config.PrimaryRegion, accountID, p.Name),
				stableCacheARN,
			},
			OwnerAccountID:       accountID,
			HumanRoleARNPatterns: humanPatterns,
		})
		if err != nil {
			return err
		}
	}

	logger.InfoContext(ctx, "release roles deployed",
		slog.String("kind", string(cfg.Kind)),
		slog.String("source_repo", config.Release.SourceRepo),
		slog.Int("release_roles", len(projects)),
	)

	return nil
}

// ReleaseTarget is one project's publish identity as configured: the
// repository whose workflows may assume the role, and the subject GitHub
// must sign for that to happen. The drift check (githubctl subjects)
// reads the same rows the deploy does, so what is verified and what is
// deployed cannot be two different lists.
type (
	ReleaseTarget struct {
		Project string
		Repo    string
		Prefix  string
		Subject string
	}
)

// releaseSubject is the one expression that decides WHICH GitHub identity
// may publish a project — a trust boundary, so it lives in exactly one
// place: widening it by accident lets another repository's workflow push
// to the stable registry, narrowing it breaks releases in a way that
// shows only at tag time. Its test called a hand-written copy of this
// derivation until the copy and the original disagreed about the very
// thing the test existed to pin.
//
// Default: the monorepo convention, one repository tagging
// "{project}/v*". A project that has moved to its own repository
// overrides the repo, the tag shape and the subject prefix
// independently — they are separate facts, and either may be overridden
// alone.
//
// The subject's HEAD is never composed from the names. GitHub signs older
// repositories as "repo:{owner}/{repo}" and ones created after its
// immutable-identifier rollout as
// "repo:{owner}@{owner_id}/{repo}@{repo_id}", and which shape a
// repository gets is GitHub's decision, not a rule we can restate.
// Composing it held for old repositories and failed for the first one created
// after the rollout, as a bare "Not authorized to perform
// sts:AssumeRoleWithWebIdentity" at tag time. So an unstated prefix is an
// ERROR here, carrying the command that answers it — a deploy that
// refuses is recoverable in a minute; a role deployed with a subject no
// token ever carries is discovered by a release that was already
// announced.
func releaseSubject(release *ReleaseConfig, project string) (ReleaseTarget, error) {
	repo, tag, prefix := release.SourceRepo, project+"/v*", release.SubjectPrefix

	if o, ok := release.Overrides[project]; ok {
		if o.SourceRepo != "" {
			repo = o.SourceRepo
		}

		if o.TagPattern != "" {
			tag = o.TagPattern
		}

		if o.SubjectPrefix != "" {
			prefix = o.SubjectPrefix
		}

		// A project publishing from its own repository cannot inherit the
		// shared prefix: that prefix belongs to a DIFFERENT repository,
		// and inheriting it would deploy a role trusting the shared repository's
		// tags under this project's name.
		if o.SourceRepo != "" && o.SubjectPrefix == "" {
			return ReleaseTarget{}, fmt.Errorf(
				"release.overrides.%s: source_repo %s without subject_prefix — "+
					"read it with: gh api /repos/%s/actions/oidc/customization/sub --jq .sub_claim_prefix",
				project, o.SourceRepo, o.SourceRepo)
		}
	}

	if prefix == "" {
		return ReleaseTarget{}, fmt.Errorf(
			"release.subject_prefix is unset — read it with: "+
				"gh api /repos/%s/actions/oidc/customization/sub --jq .sub_claim_prefix", repo)
	}

	return ReleaseTarget{
		Project: project,
		Repo:    repo,
		Prefix:  prefix,
		Subject: fmt.Sprintf("%s:ref:refs/tags/%s", prefix, tag),
	}, nil
}

// ReleaseTargets is every project publish identity the stable stack
// deploys, in project order — the list `githubctl subjects` verifies
// against GitHub.
func ReleaseTargets(config *Config) ([]ReleaseTarget, error) {
	if config.Release == nil {
		return nil, fmt.Errorf("registry config has no release block")
	}

	projects := make([]string, 0, len(config.Projects))

	for _, p := range config.Projects {
		if len(p.RepositoryNames()) > 0 {
			projects = append(projects, p.Name)
		}
	}

	sort.Strings(projects)

	targets := make([]ReleaseTarget, 0, len(projects))

	for _, name := range projects {
		t, err := releaseSubject(config.Release, name)
		if err != nil {
			return nil, err
		}

		targets = append(targets, t)
	}

	return targets, nil
}

// SubjectDrift is one project whose configured OIDC subject prefix is not
// what GitHub signs for its repository. Live == "" means the endpoint
// answered nothing usable, which is itself drift: a subject nobody can
// confirm is a subject nobody should deploy.
type (
	SubjectDrift struct {
		Project string
		Repo    string
		Want    string // what GitHub signs
		Got     string // what the configuration states
	}
)

// CompareSubjects reports the configured prefixes that disagree with the
// live ones, keyed by repository ("owner/repo").
//
// Pure, so the rule is testable without a GitHub token: the command that
// fetches is thin, and what it verifies is exactly what DeployRelease
// deploys — both walk ReleaseTargets.
func CompareSubjects(targets []ReleaseTarget, live map[string]string) []SubjectDrift {
	var drifts []SubjectDrift

	for _, t := range targets {
		if got, ok := live[t.Repo]; !ok || got != t.Prefix {
			drifts = append(drifts, SubjectDrift{
				Project: t.Project,
				Repo:    t.Repo,
				Want:    live[t.Repo],
				Got:     t.Prefix,
			})
		}
	}

	return drifts
}
