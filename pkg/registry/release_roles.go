package registry

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	// githubOIDCProviderHost is GitHub's Actions OIDC issuer. The provider is
	// registered ONCE per registry account; every release role trusts it.
	githubOIDCProviderHost = "token.actions.githubusercontent.com"

	// githubOIDCAudience is the audience configure-aws-credentials requests.
	githubOIDCAudience = "sts.amazonaws.com"

	// githubOIDCThumbprint is the certificate thumbprint AWS pins for the
	// GitHub issuer. AWS no longer verifies it for well-known IdPs, but the
	// API still requires the field.
	githubOIDCThumbprint = "6938fd4d98bab03faadb97b34396831e3780aea1"

	// IAM policy-document JSON keys/values, named so the documents below
	// read as policy rather than as string soup.
	polSid       = "Sid"
	polEffect    = "Effect"
	polAllow     = "Allow"
	polAction    = "Action"
	polPrincipal = "Principal"
	polResource  = "Resource"
	polCondition = "Condition"
	polVersion   = "Version"
	polStatement = "Statement"
	polStringEq  = "StringEquals"
	polDocDate   = "2012-10-17"
)

type (
	// ReleaseRoleConfig describes one project's publish identity in a
	// registry account.
	ReleaseRoleConfig struct {
		// Project is the business project / software-house component whose
		// artifacts this role may publish (one role per project).
		Project string

		// GitHubRepos are the repositories whose workflows may assume the
		// role, as "owner/repo". The trust condition additionally pins the
		// ref/environment (see SubjectPatterns).
		GitHubRepos []string

		// SubjectPatterns are the `sub` claim patterns allowed, e.g.
		// "repo:example/billing:ref:refs/tags/*" (releases only) or
		// "repo:example/billing:environment:release". Empty means the role
		// is unusable from CI — deliberate for a role that only humans
		// should assume.
		SubjectPatterns []string

		// RepositoryARNs are the ECR repositories this role may push to.
		RepositoryARNs []string

		// OwnerAccountID is the registry account the role lives in; it
		// anchors the human trust statement's root principal.
		OwnerAccountID string

		// HumanRoleARNPatterns are aws:PrincipalArn patterns (wildcards
		// allowed) for roles that may assume this role interactively — the
		// manual/break-glass publish path. Publishing must stay possible by
		// hand: today barctl and goreleaser publish with account
		// credentials, and once the repository policy stops granting
		// account-wide push there has to be a supported manual path — one
		// identity per project reached two ways (CI via OIDC, humans via
		// SSO) rather than a parallel set of roles that drift apart.
		//
		// Patterns, not exact ARNs, because AWSReservedSSO role names carry
		// random suffixes that change when the permission set re-provisions.
		// IAM trust-policy principals don't support wildcards, so the
		// statement names the account root as principal and pins the actual
		// caller with an aws:PrincipalArn condition — the standard pattern
		// for trusting SSO roles.
		HumanRoleARNPatterns []string

		// PermissionsBoundary caps the role (pb@ family).
		PermissionsBoundary string
	}
)

// EnsureGitHubOIDCProvider registers GitHub's Actions OIDC issuer in the
// account. Idempotent per account; returns the provider ARN for trust
// policies.
//
// The provider lives in the account that OWNS the registry, which is what
// keeps this simple: the workflow assumes a role in that same account, so
// there is no cross-account pair of policies to keep in sync, and no
// dependence on the runner's own identity. `ecr:GetAuthorizationToken` is
// not a resource-level action — it can only be granted in the caller's
// identity policy — so a repository policy alone could never authorize a
// foreign principal to log in and push.
func EnsureGitHubOIDCProvider(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	resourceName string,
) (*iam.OpenIdConnectProvider, error) {
	provider, err := iam.NewOpenIdConnectProvider(c, resourceName, &iam.OpenIdConnectProviderArgs{
		Url: pulumi.String("https://" + githubOIDCProviderHost),
		ClientIdLists: pulumi.StringArray{
			pulumi.String(githubOIDCAudience),
		},
		ThumbprintLists: pulumi.StringArray{
			pulumi.String(githubOIDCThumbprint),
		},
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("register GitHub OIDC provider: %w", err)
	}

	logger.InfoContext(c.Context(), "GitHub Actions OIDC provider registered",
		slog.String("issuer", githubOIDCProviderHost),
	)

	return provider, nil
}

// DeployReleaseRole creates one project's publish identity: an IAM role
// assumable by (a) GitHub Actions workflows of the named repos, pinned by
// `sub` to specific refs/environments, and (b) the account's human SSO
// admins. Its identity policy carries `ecr:GetAuthorizationToken` (account
// -wide by necessity — the action has no resource) plus push actions scoped
// to that project's repositories.
//
// Creating these roles changes NOTHING on its own: repository policies still
// grant the owner account push, so existing publishers keep working. Naming
// the roles in the repository policy (AccessConfig.WriterRoleARNs) and then
// removing account-wide push are separate, deliberate steps — in that order,
// after the release workflows actually assume these roles.
func DeployReleaseRole(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	oidcProviderARN pulumi.StringOutput,
	cfg ReleaseRoleConfig,
) (*iam.Role, error) {
	ctx := c.Context()

	if cfg.Project == "" {
		return nil, fmt.Errorf("release role: project is required")
	}

	if len(cfg.RepositoryARNs) == 0 {
		return nil, fmt.Errorf("release role %s: at least one repository ARN is required", cfg.Project)
	}

	roleName := fmt.Sprintf("release-%s", cfg.Project)

	assumeRolePolicy := oidcProviderARN.ApplyT(func(providerARN string) (string, error) {
		return buildReleaseTrustPolicy(providerARN, cfg)
	}).(pulumi.StringOutput)

	role, err := iam.NewRole(c, fmt.Sprintf("release-role-%s", cfg.Project), &iam.RoleArgs{
		Name:                pulumi.String(roleName),
		Description:         pulumi.Sprintf("Publish identity for %s artifacts (GitHub Actions OIDC + SSO admins)", cfg.Project),
		AssumeRolePolicy:    assumeRolePolicy,
		PermissionsBoundary: boundaryOrNil(cfg.PermissionsBoundary),
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("create release role for %s: %w", cfg.Project, err)
	}

	policyJSON, err := buildReleasePermissionPolicy(cfg.RepositoryARNs)
	if err != nil {
		return nil, fmt.Errorf("build release policy for %s: %w", cfg.Project, err)
	}

	_, err = iam.NewRolePolicy(c, fmt.Sprintf("release-policy-%s", cfg.Project), &iam.RolePolicyArgs{
		Name:   pulumi.String("ecr-publish"),
		Role:   role.Name,
		Policy: pulumi.String(policyJSON),
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("attach release policy for %s: %w", cfg.Project, err)
	}

	logger.InfoContext(ctx, "release role deployed",
		slog.String("project", cfg.Project),
		slog.String("role", roleName),
		slog.Int("github_subjects", len(cfg.SubjectPatterns)),
		slog.Int("human_principals", len(cfg.HumanRoleARNPatterns)),
		slog.Int("repositories", len(cfg.RepositoryARNs)),
	)

	return role, nil
}

// buildReleaseTrustPolicy emits the two trust statements: GitHub Actions
// (web identity, pinned by `sub`) and human SSO admins. A role with no
// subject patterns gets no CI statement at all — an unconditional
// federated trust would let ANY repository on GitHub assume it.
func buildReleaseTrustPolicy(providerARN string, cfg ReleaseRoleConfig) (string, error) {
	statements := make([]map[string]any, 0, 2)

	if len(cfg.SubjectPatterns) > 0 {
		statements = append(statements, map[string]any{
			polSid:    "GitHubActionsOIDC",
			polEffect: polAllow,
			polPrincipal: map[string]any{
				"Federated": providerARN,
			},
			polAction: "sts:AssumeRoleWithWebIdentity",
			polCondition: map[string]any{
				polStringEq: map[string]any{
					githubOIDCProviderHost + ":aud": githubOIDCAudience,
				},
				"StringLike": map[string]any{
					githubOIDCProviderHost + ":sub": cfg.SubjectPatterns,
				},
			},
		})
	}

	if len(cfg.HumanRoleARNPatterns) > 0 {
		if cfg.OwnerAccountID == "" {
			return "", fmt.Errorf("release role %s: human trust requires OwnerAccountID", cfg.Project)
		}

		statements = append(statements, map[string]any{
			polSid:    "HumanBreakGlassPublish",
			polEffect: polAllow,
			polPrincipal: map[string]any{
				"AWS": fmt.Sprintf("arn:aws:iam::%s:root", cfg.OwnerAccountID),
			},
			polAction: "sts:AssumeRole",
			polCondition: map[string]any{
				"StringLike": map[string]any{
					"aws:PrincipalArn": cfg.HumanRoleARNPatterns,
				},
			},
		})
	}

	if len(statements) == 0 {
		return "", fmt.Errorf("release role %s: no trust statements (neither CI subjects nor human principals)", cfg.Project)
	}

	doc := map[string]any{
		polVersion:   polDocDate,
		polStatement: statements,
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal trust policy: %w", err)
	}

	return string(raw), nil
}

// buildReleasePermissionPolicy grants login (account-wide by necessity) and
// push scoped to the project's repositories. Admin actions are deliberately
// absent: a publish identity must not be able to rewrite repository policy.
func buildReleasePermissionPolicy(repositoryARNs []string) (string, error) {
	doc := map[string]any{
		polVersion: polDocDate,
		polStatement: []map[string]any{
			{
				// GetAuthorizationToken has no resource — this is the only
				// way to grant `docker login`, and it is why cross-account
				// push cannot be solved with a repository policy alone.
				polSid:      "EcrLogin",
				polEffect:   polAllow,
				polAction:   "ecr:GetAuthorizationToken",
				polResource: "*",
			},
			{
				polSid:    "EcrPushScopedToProject",
				polEffect: polAllow,
				polAction: []string{
					ecrBatchCheckLayer,
					ecrPutImage,
					ecrInitiateLayerUpload,
					ecrUploadLayerPart,
					ecrCompleteLayerUpload,
					ecrGetDownloadURL,
					ecrBatchGetImage,
					ecrDescribeRepositories,
					ecrListImages,
					ecrDescribeImages,
				},
				polResource: repositoryARNs,
			},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal permission policy: %w", err)
	}

	return string(raw), nil
}

func boundaryOrNil(boundary string) pulumi.StringPtrInput {
	if boundary == "" {
		return nil
	}

	return pulumi.String(boundary)
}
