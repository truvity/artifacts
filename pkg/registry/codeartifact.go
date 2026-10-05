package registry

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	// PLAIN ASCII, and no em dash: IAM validates descriptions against
	// [\u0009\u000A\u000D\u0020-\u007E\u00A1-\u00FF], which covers
	// Latin-1 but stops well before U+2014. A dash that reads fine in a
	// comment fails CreateRole with a "ValidationError: Value at
	// 'description'" naming a regex rather than the character
	// (2026-09-06). A const so a test can hold it to that rule.
	codeArtifactRoleDescription = "Private npm (CodeArtifact) read for %s release builds - GitHub Actions OIDC, release tags only"
)

// CodeArtifactReaderOptions are the particulars of the account the reader
// roles live in, supplied by the caller.
type CodeArtifactReaderOptions struct {
	// ReadPolicyARN is the managed policy that grants the npm token and
	// nothing else (CodeArtifact's authorization token and package read).
	// It is ATTACHED rather than restated, so the npm read surface has ONE
	// definition, owned by whoever owns the registry, and these roles
	// cannot drift into a wider one. Required.
	ReadPolicyARN string

	// PermissionsBoundaryARN caps every reader role: a build identity must
	// never write IAM. Empty means no boundary.
	PermissionsBoundaryARN string
}

// DeployCodeArtifactReaderRoles gives named projects' RELEASE workflows a
// way to read the private npm registry.
//
// Why this exists at all: CodeArtifact lives in the DEVEL account, and a
// project whose images install private npm packages needs a token at build
// time. In-cluster runners have one through the pool's identity; a
// GitHub-hosted runner has no ambient identity at all — and a stable
// publish role cannot mint one, because it is scoped to ECR in another
// account. A release that builds Node images on a hosted runner is the case
// (a daemonless in-cluster builder cannot push multi-arch to an immutable
// repository: two nodes push the tag, then buildx PUTs a merged index
// under it and ECR refuses the second write).
//
// Why it does not weaken the preview tier's "no publish identities" rule:
// this grants npm READ and nothing else. It cannot push an image, write a
// chart, or touch a bucket — and it trusts EXACTLY the subject that the
// project's stable release role trusts, so a PR, a branch build, or
// another repository's workflow gets nothing. A release either has both
// halves of its identity or neither.
func DeployCodeArtifactReaderRoles(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	oidcProviderARN pulumi.StringOutput,
	accountID string,
	config *Config,
	opts CodeArtifactReaderOptions,
) error {
	ctx := c.Context()

	if config.Release == nil || len(config.Release.CodeArtifactReaders) == 0 {
		return nil
	}

	if opts.ReadPolicyARN == "" {
		return fmt.Errorf("codeartifact readers: ReadPolicyARN is required")
	}

	projects := append([]string(nil), config.Release.CodeArtifactReaders...)
	sort.Strings(projects)

	known := make(map[string]struct{}, len(config.Projects))
	for _, p := range config.Projects {
		known[p.Name] = struct{}{}
	}

	for _, project := range projects {
		// A name nobody declares would still RESOLVE: releaseSubject would
		// fall back to the estate repository and tag "{typo}/v*", minting a
		// role that trusts the monorepo's release workflows under a project
		// that does not exist. Refuse instead — the whole value of tying
		// this to the release subject is that both halves name the same
		// thing.
		if _, ok := known[project]; !ok {
			return fmt.Errorf(
				"release.codeartifact_readers names %q, which is not a project in registry.yaml", project)
		}

		target, err := releaseSubject(config.Release, project)
		if err != nil {
			return fmt.Errorf("codeartifact reader %s: %w", project, err)
		}

		// The base subject is the SAME identity the stable publish role
		// trusts (target.Subject). A repo that cuts more than one kind of
		// release tag — a project's SDKs on sdk-typescript/v* and sdk-java/v* style tags
		// alongside its backend v* — needs those extra tag refs trusted
		// too, and a job under a GitHub environment (a project's npm trusted
		// publishing) is signed with an environment subject instead of a
		// tag one. Both widen READ only.
		subjects := codeArtifactReaderSubjects(target,
			config.Release.CodeArtifactReaderExtraTags[project],
			config.Release.CodeArtifactReaderEnvironments[project])

		roleCfg := ReleaseRoleConfig{
			Project:         project,
			SubjectPatterns: subjects,
			// A build identity must never write IAM.
			PermissionsBoundary: opts.PermissionsBoundaryARN,
			OwnerAccountID:      accountID,
			// No human break-glass statement: a person who needs an npm
			// token has their own SSO profile and `just login`.
		}

		assumeRolePolicy := oidcProviderARN.ApplyT(func(providerARN string) (string, error) {
			return buildReleaseTrustPolicy(providerARN, roleCfg)
		}).(pulumi.StringOutput)

		roleName := fmt.Sprintf("release-%s-codeartifact", project)

		role, err := iam.NewRole(c, fmt.Sprintf("codeartifact-reader-%s", project), &iam.RoleArgs{
			Name:                pulumi.String(roleName),
			Description:         pulumi.Sprintf(codeArtifactRoleDescription, project),
			AssumeRolePolicy:    assumeRolePolicy,
			PermissionsBoundary: boundaryOrNil(roleCfg.PermissionsBoundary),
		}, pulumi.Provider(awsProvider))
		if err != nil {
			return fmt.Errorf("create codeartifact reader role for %s: %w", project, err)
		}

		_, err = iam.NewRolePolicyAttachment(c, fmt.Sprintf("codeartifact-reader-attach-%s", project),
			&iam.RolePolicyAttachmentArgs{
				Role:      role.Name,
				PolicyArn: pulumi.String(opts.ReadPolicyARN),
			}, pulumi.Provider(awsProvider))
		if err != nil {
			return fmt.Errorf("attach codeartifact policy for %s: %w", project, err)
		}

		logger.InfoContext(ctx, "codeartifact reader role deployed",
			slog.String("project", project),
			slog.String("role", roleName),
			slog.Any("subjects", subjects),
		)
	}

	return nil
}

// codeArtifactReaderSubjects is the trust-boundary expression for the
// read-only reader role: the base publish subject, plus one subject per
// extra tag pattern, plus one per GitHub environment. Extracted from
// DeployCodeArtifactReaderRoles so a test can pin it without a Pulumi
// context — the extra subjects are a trust widening, and widening it by
// accident is exactly what a test here prevents.
//
// Every subject reuses target.Prefix, so they name the SAME repository as
// the publish role and differ ONLY in what follows: a tag ref, or an
// environment. A base subject on a `v*` tag and extras on
// `sdk-typescript/v*` / `sdk-java/v*` let one repository's several
// release-tag shapes read the private registry, while a branch, a PR, or
// another repository still matches none.
//
// An environment subject is a different shape, not a wider one: GitHub
// signs `...:environment:<name>` in place of the ref subject whenever the
// job references that environment, and it only STARTS such a job from a
// ref the environment's deployment rule admits. The ref gate moves from
// the subject to the repository's environment settings; it does not go.
func codeArtifactReaderSubjects(target ReleaseTarget, extraTags, environments []string) []string {
	subjects := make([]string, 0, 1+len(extraTags)+len(environments))
	subjects = append(subjects, target.Subject)
	for _, extra := range extraTags {
		subjects = append(subjects, fmt.Sprintf("%s:ref:refs/tags/%s", target.Prefix, extra))
	}
	for _, env := range environments {
		subjects = append(subjects, fmt.Sprintf("%s:environment:%s", target.Prefix, env))
	}
	return subjects
}
