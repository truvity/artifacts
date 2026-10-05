package registry

import (
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	// S3LifecyclePolicy defines S3 object retention rules
	S3LifecyclePolicy struct {
		// TransitionToIADays transitions objects to Infrequent Access storage class after N days
		// Set to 0 to disable transition to IA
		TransitionToIADays int

		// DevelNoncurrentVersionExpirationDays expires non-current versions of devel objects after N days
		// Set to 0 to disable devel version cleanup
		// AWS minimum is 1 day
		DevelNoncurrentVersionExpirationDays int

		// CleanupUntaggedNoncurrentVersionExpirationDays expires non-current versions of untagged objects after N days
		// Set to 0 to disable untagged version cleanup
		// AWS minimum is 1 day
		CleanupUntaggedNoncurrentVersionExpirationDays int
	}
)

// DefaultPreviewS3Lifecycle returns S3 lifecycle policy for preview buckets
// Preview buckets clean up old devel versions and transition to IA for cost optimization
func DefaultPreviewS3Lifecycle() S3LifecyclePolicy {
	return S3LifecyclePolicy{
		TransitionToIADays:                             30, // Transition to IA after 30 days
		DevelNoncurrentVersionExpirationDays:           1,  // Expire old devel versions after 1 day (AWS minimum)
		CleanupUntaggedNoncurrentVersionExpirationDays: 1,  // Clean up untagged versions after 1 day
	}
}

// DefaultStableS3Lifecycle returns S3 lifecycle policy for stable buckets
// Stable buckets keep everything (append-only for audit trail) but still transition to IA
func DefaultStableS3Lifecycle() S3LifecyclePolicy {
	return S3LifecyclePolicy{
		TransitionToIADays:                             30, // Transition to IA after 30 days for cost optimization
		DevelNoncurrentVersionExpirationDays:           0,  // No cleanup
		CleanupUntaggedNoncurrentVersionExpirationDays: 0,  // Keep untagged versions for audit
	}
}

// ConfigureS3Lifecycle applies lifecycle policy to an S3 bucket
// This is the "Lifecycle" layer - mirrors ECR lifecycle behavior
func ConfigureS3Lifecycle(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	bucket *s3.Bucket,
	resourceName string,
	policy S3LifecyclePolicy,
) error {
	ctx := c.Context()

	// If no cleanup is needed and no transitions, skip lifecycle policy
	if policy.TransitionToIADays == 0 &&
		policy.DevelNoncurrentVersionExpirationDays == 0 &&
		policy.CleanupUntaggedNoncurrentVersionExpirationDays == 0 {
		logger.InfoContext(ctx, "skipping S3 lifecycle policy (append-only mode, no transitions)",
			slog.String("bucket", resourceName),
		)
		return nil
	}

	// Build lifecycle rules
	rules := s3.BucketLifecycleConfigurationV2RuleArray{}

	// Rule 1: Transition to IA after N days (cost optimization)
	// This applies to all objects, but releases are kept forever
	if policy.TransitionToIADays > 0 {
		rules = append(rules, &s3.BucketLifecycleConfigurationV2RuleArgs{
			Id:     pulumi.String("TransitionToIA"),
			Status: pulumi.String("Enabled"),
			Transitions: s3.BucketLifecycleConfigurationV2RuleTransitionArray{
				&s3.BucketLifecycleConfigurationV2RuleTransitionArgs{
					Days:         pulumi.Int(policy.TransitionToIADays),
					StorageClass: pulumi.String("STANDARD_IA"),
				},
			},
		})
	}

	// Rule 2: Keep pre-release tags forever (no expiration rule needed)
	// This is implicit - no rule means keep forever

	// Rule 3: Expire non-current versions of devel archives
	if policy.DevelNoncurrentVersionExpirationDays > 0 {
		rules = append(rules, &s3.BucketLifecycleConfigurationV2RuleArgs{
			Id:     pulumi.String("ExpireDevelVersions"),
			Status: pulumi.String("Enabled"),
			// All objects — expressed via filter: bare rule.prefix is
			// deprecated by the provider (warning on every deploy).
			Filter: &s3.BucketLifecycleConfigurationV2RuleFilterArgs{
				Prefix: pulumi.String(""),
			},
			NoncurrentVersionExpiration: &s3.BucketLifecycleConfigurationV2RuleNoncurrentVersionExpirationArgs{
				NoncurrentDays: pulumi.Int(policy.DevelNoncurrentVersionExpirationDays),
			},
			// Note: S3 lifecycle doesn't support tag-based filtering like ECR
			// We'll rely on object key patterns: {project}/{component}/v*-dev.*/{arch}.zip
			// This is a simplified approach - full pattern matching would require custom Lambda
		})
	}

	// Rule 4: Clean up untagged/old versions
	if policy.CleanupUntaggedNoncurrentVersionExpirationDays > 0 {
		rules = append(rules, &s3.BucketLifecycleConfigurationV2RuleArgs{
			Id:     pulumi.String("CleanupOldVersions"),
			Status: pulumi.String("Enabled"),
			NoncurrentVersionExpiration: &s3.BucketLifecycleConfigurationV2RuleNoncurrentVersionExpirationArgs{
				NoncurrentDays: pulumi.Int(policy.CleanupUntaggedNoncurrentVersionExpirationDays),
			},
		})
	}

	// Create lifecycle configuration
	_, err := s3.NewBucketLifecycleConfigurationV2(c, fmt.Sprintf("%s-lifecycle", resourceName), &s3.BucketLifecycleConfigurationV2Args{
		Bucket: bucket.ID(),
		Rules:  rules,
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return fmt.Errorf("create S3 lifecycle policy: %w", err)
	}

	logger.InfoContext(ctx, "S3 lifecycle policy applied",
		slog.String("bucket", resourceName),
		slog.Int("transition_to_ia_days", policy.TransitionToIADays),
		slog.Int("devel_version_expiration_days", policy.DevelNoncurrentVersionExpirationDays),
		slog.Int("untagged_version_expiration_days", policy.CleanupUntaggedNoncurrentVersionExpirationDays),
	)

	return nil
}
