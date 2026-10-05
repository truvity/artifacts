package registry

import (
	"fmt"
	"log/slog"
	"slices"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	// DeployECRConfig holds configuration for ECR repository deployment.
	DeployECRConfig struct {
		// Project to deploy
		Project *ProjectConfig

		// Region is the AWS region for ECR repositories
		Region string

		// RepositoryKind: preview or stable
		Kind RepositoryKind

		// AWSProvider is the pre-created AWS provider for this region
		AWSProvider *aws.Provider

		// AccountID is the AWS account ID (owner)
		AccountID string

		// Reader accounts for cross-account access
		ReaderAccounts ReaderAccountIDs

		// Retiring names the project's ECR components marked for removal;
		// only those repositories get force-delete (Config.ForceDelete).
		Retiring []string
	}

	// DeployS3Config holds configuration for S3 bucket deployment
	DeployS3Config struct {
		// Region is the AWS region for the S3 bucket
		Region string

		// RepositoryKind: preview or stable
		Kind RepositoryKind

		// AWSProvider is the pre-created AWS provider for this region
		AWSProvider *aws.Provider

		// AccountID is the AWS account ID (owner) the bucket lives in
		AccountID string

		// Reader accounts for cross-account access
		ReaderAccounts ReaderAccountIDs
	}
)

// DeployECR creates ECR repositories for a single project with lifecycle and IAM policies.
// The AWS provider must be pre-created by the caller to avoid duplicate provider URNs
// when deploying to multiple regions.
func DeployECR(c *pulumi.Context, logger *slog.Logger, cfg DeployECRConfig) error {
	ctx := c.Context()

	repoNames := cfg.Project.RepositoryNames()

	logger.InfoContext(ctx, "deploying ECR repositories for project",
		slog.String("project", cfg.Project.Name),
		slog.String("kind", string(cfg.Kind)),
		slog.String("region", cfg.Region),
		slog.Int("ecr_repositories", len(repoNames)),
	)

	// Determine policies based on repository kind
	tagMutability := TagMutabilityForKind(cfg.Kind)
	tagMutabilityExclusionFilters := TagMutabilityExclusionFiltersForKind(cfg.Kind)
	lifecyclePolicy := ECRLifecyclePolicyForKind(cfg.Kind)
	readerAccounts := DefaultReaderAccountsForKind(cfg.Kind, cfg.ReaderAccounts)

	logger.InfoContext(ctx, "repository configuration",
		slog.String("tag_mutability", tagMutability),
		slog.Int("exclusion_filters", len(tagMutabilityExclusionFilters)),
		slog.Int("reader_accounts", len(readerAccounts)),
	)

	// Deploy ECR repositories
	for i, repoName := range repoNames {
		component := cfg.Project.ECR[i]
		if err := deployECRRepository(
			c, logger, cfg.AWSProvider,
			repoName, cfg.Region, cfg.AccountID,
			tagMutability, tagMutabilityExclusionFilters, lifecyclePolicy, readerAccounts,
			slices.Contains(cfg.Retiring, component),
		); err != nil {
			return fmt.Errorf("deploy ECR repository %s: %w", repoName, err)
		}
	}

	logger.InfoContext(ctx, "ECR repositories deployment completed",
		slog.String("project", cfg.Project.Name),
		slog.Int("ecr_repositories_created", len(repoNames)),
	)

	return nil
}

// DeployS3 creates S3 bucket for Lambda archives (not project-specific)
// Single bucket per kind (preview/stable) per region, shared across all projects.
// Call this function once per region to create buckets in multiple regions.
func DeployS3(c *pulumi.Context, logger *slog.Logger, cfg DeployS3Config) error {
	ctx := c.Context()

	logger.InfoContext(ctx, "deploying S3 bucket for Lambda archives",
		slog.String("kind", string(cfg.Kind)),
		slog.String("region", cfg.Region),
		slog.String("account_id", cfg.AccountID),
	)

	accountID := cfg.AccountID
	awsProvider := cfg.AWSProvider

	// Determine policies based on repository kind
	lifecyclePolicy := S3LifecyclePolicyForKind(cfg.Kind)
	readerAccounts := DefaultReaderAccountsForKind(cfg.Kind, cfg.ReaderAccounts)

	// Deploy S3 bucket (single bucket per kind per region, shared across all projects)
	// Bucket name is generated from accountID and region (not project-specific)
	bucketResourceName := fmt.Sprintf("artifacts-%s-%s", string(cfg.Kind), cfg.Region)
	bucket, err := deployS3Bucket(
		c, logger, awsProvider,
		bucketResourceName, accountID, cfg.Region,
		lifecyclePolicy, readerAccounts,
	)
	if err != nil {
		return fmt.Errorf("deploy S3 bucket: %w", err)
	}

	_ = bucket // Used for exports

	logger.InfoContext(ctx, "S3 bucket deployment completed",
		slog.String("kind", string(cfg.Kind)),
		slog.String("bucket_name", fmt.Sprintf("artifacts-%s-%s", accountID, cfg.Region)),
	)

	return nil
}

// deployECRRepository creates a single ECR repository with all layers.
// region is included in Pulumi resource names to avoid conflicts when deploying
// the same ECR repository in multiple regions (e.g., during migration).
func deployECRRepository(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	name string,
	region string,
	ownerAccountID string,
	tagMutability string,
	tagMutabilityExclusionFilters []string,
	lifecyclePolicy ECRLifecyclePolicy,
	readerAccounts []string,
	forceDelete bool,
) error {
	ctx := c.Context()

	// Pulumi resource names use /<region> suffix for consistent multi-region naming
	repoResourceName := fmt.Sprintf("%s/%s", name, region)

	logger.InfoContext(ctx, "creating ECR repository",
		slog.String("repository", name),
		slog.String("region", region),
		slog.String("resource_name", repoResourceName),
	)

	// Layer 1: Create repository
	repository, err := CreateRepository(c, logger, awsProvider, repoResourceName, name, tagMutability, tagMutabilityExclusionFilters, false, forceDelete)
	if err != nil {
		return fmt.Errorf("create repository: %w", err)
	}

	// Layer 2: Configure lifecycle
	lifecycleResourceName := fmt.Sprintf("%s/lifecycle/%s", name, region)
	if err := ConfigureLifecycle(c, logger, awsProvider, repository, lifecycleResourceName, lifecyclePolicy); err != nil {
		return fmt.Errorf("configure lifecycle: %w", err)
	}

	// Layer 3: Configure access (IAM)
	policyResourceName := fmt.Sprintf("%s/policy/%s", name, region)
	accessConfig := AccessConfig{
		OwnerAccountID: ownerAccountID,
		ReaderAccounts: readerAccounts,
	}
	if err := ConfigureAccess(c, logger, awsProvider, repository, policyResourceName, accessConfig); err != nil {
		return fmt.Errorf("configure access: %w", err)
	}

	// Export repository URL
	ExportRepositoryURL(c, repository, repoResourceName)

	return nil
}

// deployS3Bucket creates an S3 bucket with lifecycle and access policies.
func deployS3Bucket(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	name string,
	accountID string,
	region string,
	lifecyclePolicy S3LifecyclePolicy,
	readerAccounts []string,
) (*s3.Bucket, error) {
	ctx := c.Context()

	logger.InfoContext(ctx, "creating S3 bucket",
		slog.String("bucket_name", name),
		slog.String("region", region),
	)

	// Layer 1: Create bucket
	bucket, err := CreateS3Bucket(c, logger, awsProvider, name, accountID, region)
	if err != nil {
		return nil, fmt.Errorf("create bucket: %w", err)
	}

	// Layer 2: Configure lifecycle
	if err := ConfigureS3Lifecycle(c, logger, awsProvider, bucket, name, lifecyclePolicy); err != nil {
		return nil, fmt.Errorf("configure S3 lifecycle: %w", err)
	}

	// Layer 3: Configure access (IAM)
	accessConfig := AccessConfig{
		OwnerAccountID: accountID,
		ReaderAccounts: readerAccounts,
	}
	if err := ConfigureS3Access(c, logger, awsProvider, bucket, name, accessConfig); err != nil {
		return nil, fmt.Errorf("configure S3 access: %w", err)
	}

	// Export bucket URL
	ExportS3BucketURL(c, bucket, name)

	return bucket, nil
}
