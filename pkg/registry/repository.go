package registry

import (
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CreateRepository creates a single ECR repository.
// This is the "Create" layer - just creates the repository without lifecycle or IAM policies.
// resourceName is the Pulumi resource name (must be unique per stack, includes region suffix).
// name is the ECR repository name in AWS (e.g., "project/image").
func CreateRepository(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	resourceName string,
	name string,
	tagMutability string,
	tagMutabilityExclusionFilters []string,
	scanOnPush bool,
	forceDelete bool,
) (*ecr.Repository, error) {
	ctx := c.Context()

	logger.DebugContext(ctx, "creating ECR repository",
		slog.String("repository", name),
		slog.String("resource_name", resourceName),
		slog.String("tag_mutability", tagMutability),
		slog.Int("exclusion_filters", len(tagMutabilityExclusionFilters)),
		slog.Bool("scan_on_push", scanOnPush),
	)

	// Build exclusion filters if provided
	var exclusionFilters ecr.RepositoryImageTagMutabilityExclusionFilterArray
	if len(tagMutabilityExclusionFilters) > 0 {
		for _, filter := range tagMutabilityExclusionFilters {
			exclusionFilters = append(exclusionFilters, ecr.RepositoryImageTagMutabilityExclusionFilterArgs{
				FilterType: pulumi.String("WILDCARD"),
				Filter:     pulumi.String(filter),
			})
		}
		logger.InfoContext(ctx, "configured tag mutability exclusions",
			slog.String("repository", name),
			slog.Int("filter_count", len(tagMutabilityExclusionFilters)),
		)
		for i, filter := range tagMutabilityExclusionFilters {
			logger.DebugContext(ctx, "exclusion filter",
				slog.String("repository", name),
				slog.Int("filter_index", i),
				slog.String("filter", filter),
			)
		}
	}

	// Create ECR repository
	repositoryArgs := &ecr.RepositoryArgs{
		Name:               pulumi.String(name),
		ImageTagMutability: pulumi.String(tagMutability),
		// Only a repository marked retiring (ProjectConfig.ForceDelete); a
		// non-empty one cannot be deleted without it, and Pulumi needs the
		// flag in state before the delete.
		ForceDelete: pulumi.Bool(forceDelete),
		ImageScanningConfiguration: &ecr.RepositoryImageScanningConfigurationArgs{
			ScanOnPush: pulumi.Bool(scanOnPush),
		},
	}

	// Add exclusion filters if tag mutability is IMMUTABLE_WITH_EXCLUSION
	if tagMutability == "IMMUTABLE_WITH_EXCLUSION" && len(exclusionFilters) > 0 {
		repositoryArgs.ImageTagMutabilityExclusionFilters = exclusionFilters
	}

	repository, err := ecr.NewRepository(c, resourceName, repositoryArgs, pulumi.Provider(awsProvider))
	if err != nil {
		return nil, fmt.Errorf("create repository %s (resource: %s): %w", name, resourceName, err)
	}

	logger.InfoContext(ctx, "created ECR repository",
		slog.String("repository", name),
		slog.String("resource_name", resourceName),
		slog.String("tag_mutability", tagMutability),
		slog.Int("exclusion_filters", len(tagMutabilityExclusionFilters)),
	)

	return repository, nil
}

// SanitizeExportName sanitizes a repository name for use in Pulumi exports
// Replaces / with _ and - with _
func SanitizeExportName(name string) string {
	result := ""
	for _, ch := range name {
		if ch == '/' || ch == '-' {
			result += "_"
		} else {
			result += string(ch)
		}
	}
	return result
}

// ExportRepositoryURL exports the repository URL with a sanitized name
func ExportRepositoryURL(c *pulumi.Context, repository *ecr.Repository, exportName string) {
	sanitized := SanitizeExportName(exportName)
	c.Export(fmt.Sprintf("repository_%s_url", sanitized), repository.RepositoryUrl)
}
