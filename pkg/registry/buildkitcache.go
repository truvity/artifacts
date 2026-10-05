package registry

import (
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CIBuildkitCacheRepo is the WARM layer of the BuildKit cache plane
// (docs/architecture/ci.md § Cache plane): `--cache-to
// type=registry,mode=max` from the snapshot pipelines lands here, so a
// replaced builder (its PVC is only the HOT layer) starts warm instead
// of cold. Platform infrastructure like ci/runners — no business
// project owns it, no release identity is minted for it: in the preview
// account pushes ride the runner pool's pod identity (esoiam plumbing
// grant); in the stable account every project's release role
// is granted the repository alongside its own (release_stack.go).
const (
	CIBuildkitCacheRepo = "ci/buildkit-cache"
)

// DeployCIBuildkitCache creates the warm-cache repository with an
// aggressive lifecycle: cache manifests are re-pushed on every build
// (mutable moving tags per project), so anything untagged is a dead
// layer within days.
func DeployCIBuildkitCache(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
) error {
	repo, err := CreateRepository(c, logger, awsProvider,
		"ci-buildkit-cache", CIBuildkitCacheRepo, "MUTABLE", nil, true, false)
	if err != nil {
		return fmt.Errorf("create buildkit cache repo: %w", err)
	}

	const policy = `{
  "rules": [
    {
      "rulePriority": 1,
      "description": "expire untagged cache layers after 7 days",
      "selection": {
        "tagStatus": "untagged",
        "countType": "sinceImagePushed",
        "countUnit": "days",
        "countNumber": 7
      },
      "action": {"type": "expire"}
    }
  ]
}`

	_, err = ecr.NewLifecyclePolicy(c, "ci-buildkit-cache-lifecycle",
		&ecr.LifecyclePolicyArgs{
			Repository: repo.Name,
			Policy:     pulumi.String(policy),
		}, pulumi.Provider(awsProvider))
	if err != nil {
		return fmt.Errorf("attach cache lifecycle policy: %w", err)
	}

	return nil
}
