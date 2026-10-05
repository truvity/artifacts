package registry

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecr"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	// AccessConfig defines who can access the repository
	AccessConfig struct {
		OwnerAccountID string   // Full access (push/pull/admin)
		ReaderAccounts []string // Read-only access (pull only)
		WriterAccounts []string // Write access (push/pull, no admin) - future use

		// WriterRoleARNs are individual IAM roles allowed to PUSH to this
		// repository — the fine-grained half of the tier-symmetric registry
		// model: identical repo structure across devel → stage →
		// prod, with permissions expressing the tier. devel-tier repos stay
		// broadly writable by their account; stable-tier repos name the
		// per-business-project / per-software-house roles that may publish,
		// and those roles are exactly what the GitHub-OIDC release workflows
		// assume. A mistyped push target then fails on IAM with CloudTrail
		// attribution instead of landing in the wrong repository.
		//
		// Empty (today's state) = no extra writer statement, so behavior is
		// unchanged. Owner-account push is NOT removed by populating this:
		// tightening that is a deliberate follow-up once the release
		// workflows actually assume these roles, because barctl/goreleaser
		// publish with account credentials today and would break instantly.
		WriterRoleARNs []string
	}
)

// ConfigureAccess applies IAM policy to an ECR repository
// This is the "IAM" layer - manages who can read/write/admin the repository
func ConfigureAccess(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	repository *ecr.Repository,
	resourceName string,
	access AccessConfig,
) error {
	ctx := c.Context()

	// Build owner account ARN
	ownerAccountARN := fmt.Sprintf("arn:aws:iam::%s:root", access.OwnerAccountID)

	// Build policy statements using IAM policy document library types for type safety
	effectAllow := string(iam.PolicyStatementEffectALLOW)
	statements := []iam.GetPolicyDocumentStatement{
		// Statement 1: Owner account full access
		{
			Sid:    pulumi.StringRef("AllowOwnerAccountAccess"),
			Effect: &effectAllow,
			Principals: []iam.GetPolicyDocumentStatementPrincipal{
				{
					Type:        principalAWS,
					Identifiers: []string{ownerAccountARN},
				},
			},
			Actions: []string{
				ecrGetDownloadURL,
				ecrBatchGetImage,
				ecrBatchCheckLayer,
				ecrPutImage,
				ecrInitiateLayerUpload,
				ecrUploadLayerPart,
				ecrCompleteLayerUpload,
				ecrDescribeRepositories,
				"ecr:GetRepositoryPolicy",
				ecrListImages,
				ecrDescribeImages,
			},
		},
	}

	// Statement 2: Cross-account read-only access (pull only)
	if len(access.ReaderAccounts) > 0 {
		readerPrincipals := make([]string, len(access.ReaderAccounts))
		for i, accountID := range access.ReaderAccounts {
			readerPrincipals[i] = fmt.Sprintf("arn:aws:iam::%s:root", accountID)
		}

		statements = append(statements, iam.GetPolicyDocumentStatement{
			Sid:    pulumi.StringRef("AllowCrossAccountPull"),
			Effect: &effectAllow,
			Principals: []iam.GetPolicyDocumentStatementPrincipal{
				{
					Type:        principalAWS,
					Identifiers: readerPrincipals,
				},
			},
			Actions: []string{
				ecrGetDownloadURL,
				ecrBatchGetImage,
				ecrBatchCheckLayer,
				ecrDescribeRepositories,
				ecrListImages,
				ecrDescribeImages,
			},
		})

		logger.InfoContext(ctx, "ECR repository policy includes cross-account read access",
			slog.String("repository", resourceName),
			slog.Int("reader_accounts", len(access.ReaderAccounts)),
		)
	}

	// Statement 2b: named writer roles (push, no admin). See
	// AccessConfig.WriterRoleARNs — the fine-grained publish identity.
	if len(access.WriterRoleARNs) > 0 {
		statements = append(statements, iam.GetPolicyDocumentStatement{
			Sid:    pulumi.StringRef("AllowNamedWriterRolesPush"),
			Effect: &effectAllow,
			Principals: []iam.GetPolicyDocumentStatementPrincipal{
				{
					Type:        principalAWS,
					Identifiers: access.WriterRoleARNs,
				},
			},
			Actions: []string{
				ecrGetDownloadURL,
				ecrBatchGetImage,
				ecrBatchCheckLayer,
				ecrPutImage,
				ecrInitiateLayerUpload,
				ecrUploadLayerPart,
				ecrCompleteLayerUpload,
				ecrDescribeRepositories,
				ecrListImages,
				ecrDescribeImages,
			},
		})

		logger.InfoContext(ctx, "ECR repository policy includes named writer roles",
			slog.String("repository", resourceName),
			slog.Int("writer_roles", len(access.WriterRoleARNs)),
		)
	}

	// Statement 3: Lambda service access from reader accounts
	for _, accountID := range access.ReaderAccounts {
		statements = append(statements, iam.GetPolicyDocumentStatement{
			Sid:    pulumi.StringRef(fmt.Sprintf("AllowLambdaServicePullFrom%s", accountID)),
			Effect: &effectAllow,
			Principals: []iam.GetPolicyDocumentStatementPrincipal{
				{
					Type:        "Service",
					Identifiers: []string{"lambda.amazonaws.com"},
				},
			},
			Conditions: []iam.GetPolicyDocumentStatementCondition{
				{
					Test:     "StringEquals",
					Variable: "aws:SourceAccount",
					Values:   []string{accountID},
				},
			},
			Actions: []string{
				ecrGetDownloadURL,
				ecrBatchGetImage,
				ecrBatchCheckLayer,
			},
		})
	}

	if len(access.ReaderAccounts) > 0 {
		logger.InfoContext(ctx, "ECR repository policy includes Lambda service access",
			slog.String("repository", resourceName),
			slog.Int("lambda_accounts", len(access.ReaderAccounts)),
		)
	}

	// Convert to JSON for the policy
	policyJSON, err := buildPolicyJSON(statements)
	if err != nil {
		return fmt.Errorf("build policy JSON: %w", err)
	}

	// Apply repository policy
	_, err = ecr.NewRepositoryPolicy(c, resourceName, &ecr.RepositoryPolicyArgs{
		Repository: repository.Name,
		Policy:     pulumi.String(policyJSON),
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return fmt.Errorf("create repository policy: %w", err)
	}

	logger.InfoContext(ctx, "ECR repository policy applied",
		slog.String("repository", resourceName),
	)

	return nil
}

// buildPolicyJSON converts IAM statements to JSON policy document
func buildPolicyJSON(statements []iam.GetPolicyDocumentStatement) (string, error) {
	type jsonStatement struct {
		Sid       *string        `json:"Sid,omitempty"`
		Effect    string         `json:"Effect"`
		Principal map[string]any `json:"Principal"`
		Action    []string       `json:"Action"`
		Condition map[string]any `json:"Condition,omitempty"`
	}

	jsonStatements := make([]jsonStatement, 0, len(statements))
	for i := range statements {
		stmt := &statements[i]
		jsonStmt := jsonStatement{
			Sid:    stmt.Sid,
			Effect: *stmt.Effect,
			Principal: map[string]any{
				stmt.Principals[0].Type: stmt.Principals[0].Identifiers,
			},
			Action: stmt.Actions,
		}

		if len(stmt.Conditions) > 0 {
			jsonStmt.Condition = map[string]any{
				stmt.Conditions[0].Test: map[string]any{
					stmt.Conditions[0].Variable: stmt.Conditions[0].Values,
				},
			}
		}

		jsonStatements = append(jsonStatements, jsonStmt)
	}

	// Build policy document
	policyDoc := map[string]any{
		"Version":   "2012-10-17",
		"Statement": jsonStatements,
	}

	jsonBytes, err := json.Marshal(policyDoc)
	if err != nil {
		return "", fmt.Errorf("marshal policy document: %w", err)
	}

	return string(jsonBytes), nil
}
