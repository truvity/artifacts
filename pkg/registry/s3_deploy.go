package registry

import (
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CreateS3Bucket creates a single S3 bucket for Lambda archives
// This is the "Create" layer - creates the bucket with versioning, encryption, and public access block
func CreateS3Bucket(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	name string,
	accountID string,
	region string,
) (*s3.Bucket, error) {
	ctx := c.Context()

	bucketName := ArtifactsBucketName(accountID, region)

	logger.DebugContext(ctx, "creating S3 bucket",
		slog.String("bucket_name", bucketName),
		slog.String("account_id", accountID),
		slog.String("region", region),
	)

	// Create S3 bucket
	bucket, err := s3.NewBucket(c, name, &s3.BucketArgs{
		Bucket: pulumi.String(bucketName),
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("create S3 bucket %s: %w", bucketName, err)
	}

	// Enable versioning (SDK v7: separate resource)
	_, err = s3.NewBucketVersioningV2(c, fmt.Sprintf("%s-versioning", name), &s3.BucketVersioningV2Args{
		Bucket: bucket.ID(),
		VersioningConfiguration: &s3.BucketVersioningV2VersioningConfigurationArgs{
			Status: pulumi.String("Enabled"),
		},
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("enable bucket versioning: %w", err)
	}

	// Configure encryption (SDK v7: separate resource)
	_, err = s3.NewBucketServerSideEncryptionConfigurationV2(c, fmt.Sprintf("%s-encryption", name), &s3.BucketServerSideEncryptionConfigurationV2Args{
		Bucket: bucket.ID(),
		Rules: s3.BucketServerSideEncryptionConfigurationV2RuleArray{
			&s3.BucketServerSideEncryptionConfigurationV2RuleArgs{
				ApplyServerSideEncryptionByDefault: &s3.BucketServerSideEncryptionConfigurationV2RuleApplyServerSideEncryptionByDefaultArgs{
					SseAlgorithm: pulumi.String("AES256"),
				},
			},
		},
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("configure bucket encryption: %w", err)
	}

	// Prevent accidental public access (SDK v7: separate resource)
	_, err = s3.NewBucketPublicAccessBlock(c, fmt.Sprintf("%s-public-access", name), &s3.BucketPublicAccessBlockArgs{
		Bucket:                bucket.ID(),
		BlockPublicAcls:       pulumi.Bool(true),
		BlockPublicPolicy:     pulumi.Bool(true),
		IgnorePublicAcls:      pulumi.Bool(true),
		RestrictPublicBuckets: pulumi.Bool(true),
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("configure public access block: %w", err)
	}

	// NOTE: Bucket policy (HTTPS-only + cross-account access) is applied by
	// ConfigureS3Access as a single unified policy. Do NOT create a separate
	// BucketPolicy here — S3 only supports one policy per bucket, so a second
	// BucketPolicy resource would overwrite the access policy.

	logger.InfoContext(ctx, "created S3 bucket",
		slog.String("bucket_name", bucketName),
	)

	return bucket, nil
}

// ExportS3BucketURL exports the bucket URL with a sanitized name
func ExportS3BucketURL(c *pulumi.Context, bucket *s3.Bucket, exportName string) {
	sanitized := SanitizeExportName(exportName)
	c.Export(fmt.Sprintf("bucket_%s_url", sanitized), bucket.BucketDomainName)
}

// ArtifactsBucketName constructs the S3 bucket name for artifact storage.
// Pattern: artifacts-{accountID}-{region}.
func ArtifactsBucketName(accountID, region string) string {
	return fmt.Sprintf("artifacts-%s-%s", accountID, region)
}
