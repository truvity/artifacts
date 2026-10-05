package registry

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// ConfigureS3Access applies bucket policy to an S3 bucket
// This is the "IAM" layer - manages who can read/write the bucket
// Mirrors ECR IAM patterns for similar cross-account access
func ConfigureS3Access(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	bucket *s3.Bucket,
	resourceName string,
	access AccessConfig,
) error {
	ctx := c.Context()

	// Build owner account ARN
	ownerAccountARN := fmt.Sprintf("arn:aws:iam::%s:root", access.OwnerAccountID)

	// Build policy document using bucket ARN
	// We need to build the policy JSON with proper resource ARNs
	policyJSON := bucket.Arn.ApplyT(func(bucketARN string) (string, error) {
		// Build policy statements
		effectAllow := string(iam.PolicyStatementEffectALLOW)
		effectDeny := string(iam.PolicyStatementEffectDENY)
		statements := []iam.GetPolicyDocumentStatement{
			// Statement 0: Deny non-HTTPS access (transport security)
			{
				Sid:    pulumi.StringRef("DenyNonHTTPS"),
				Effect: &effectDeny,
				Principals: []iam.GetPolicyDocumentStatementPrincipal{
					{
						Type:        principalAWS,
						Identifiers: []string{"*"},
					},
				},
				Actions: []string{"s3:*"},
				Resources: []string{
					bucketARN,
					bucketARN + "/*",
				},
				Conditions: []iam.GetPolicyDocumentStatementCondition{
					{
						Test:     "Bool",
						Variable: "aws:SecureTransport",
						Values:   []string{"false"},
					},
				},
			},
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
					s3GetObject,
					"s3:PutObject",
					"s3:DeleteObject",
					"s3:ListBucket",
				},
				Resources: []string{
					bucketARN,
					bucketARN + "/*",
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
					s3GetObject,
					"s3:ListBucket",
				},
				Resources: []string{
					bucketARN,
					bucketARN + "/*",
				},
			})

			logger.InfoContext(ctx, "S3 bucket policy includes cross-account read access",
				slog.String("bucket", resourceName),
				slog.Int("reader_accounts", len(access.ReaderAccounts)),
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
					s3GetObject,
				},
				Resources: []string{
					bucketARN + "/*",
				},
			})
		}

		if len(access.ReaderAccounts) > 0 {
			logger.InfoContext(ctx, "S3 bucket policy includes Lambda service access",
				slog.String("bucket", resourceName),
				slog.Int("lambda_accounts", len(access.ReaderAccounts)),
			)
		}

		// Convert to JSON
		return buildS3PolicyJSONFromStatements(statements)
	}).(pulumi.StringOutput)

	if len(access.ReaderAccounts) > 0 {
		logger.InfoContext(ctx, "S3 bucket policy includes Lambda service access",
			slog.String("bucket", resourceName),
			slog.Int("lambda_accounts", len(access.ReaderAccounts)),
		)
	}

	// Apply bucket policy
	_, err := s3.NewBucketPolicy(c, resourceName+"-policy", &s3.BucketPolicyArgs{
		Bucket: bucket.ID(),
		Policy: policyJSON,
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return fmt.Errorf("create bucket policy: %w", err)
	}

	logger.InfoContext(ctx, "S3 bucket policy applied",
		slog.String("bucket", resourceName),
	)

	return nil
}

// buildS3PolicyJSONFromStatements converts IAM statements to JSON policy document for S3
func buildS3PolicyJSONFromStatements(statements []iam.GetPolicyDocumentStatement) (string, error) {
	type jsonStatement struct {
		Sid       *string        `json:"Sid,omitempty"`
		Effect    string         `json:"Effect"`
		Principal map[string]any `json:"Principal"`
		Action    []string       `json:"Action"`
		Resource  []string       `json:"Resource"`
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
			Action:   stmt.Actions,
			Resource: stmt.Resources,
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
