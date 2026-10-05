package registry

import (
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	// ECRLifecyclePolicy defines ECR image retention for one repository kind.
	//
	// Deliberately a single count, with NO tag patterns. The previous shape
	// carried three pattern-matched rules — keep `v*-rc.*` forever, keep the
	// last 10 `v*-dev.*`, expire untagged beyond 1 — and pruned NOTHING for
	// months, because the images are tagged `1.21.3-47b33c45-nightly`: no `v`
	// prefix, and `-nightly` is not `-dev.`. A rule keyed to a naming habit
	// fails silently the moment the habit changes, and reports no error while
	// doing so.
	//
	// The untagged rule could not compensate either. A multi-arch build
	// publishes one tagged manifest list plus ~4 children (measured: 5.01
	// images per build on url-shortener/log), and ECR will not expire a child
	// that a live manifest still references — so untagged cleanup is blocked
	// for exactly as long as the parent tags survive, which was forever.
	//
	// Counting every image, tagged or not, has neither failure mode.
	ECRLifecyclePolicy struct {
		// KeepLast is how many images to retain, newest first, regardless of
		// tag. Zero means append-only: no policy is written at all.
		//
		// Note the unit is IMAGES, not builds. At ~5 images per multi-arch
		// build, 750 images is roughly 150 builds.
		KeepLast int
	}
)

// DefaultPreviewECRLifecycle is the retention for snapshot registries
// (the development account): keep a deep but bounded history.
//
// 750 was chosen to keep as much as practical while ending unbounded growth —
// one development account accumulated 42.7 GB in a single day at the observed build cadence.
func DefaultPreviewECRLifecycle() ECRLifecyclePolicy {
	return ECRLifecyclePolicy{KeepLast: 750}
}

// DefaultStableECRLifecycle is the retention for release registries: none.
//
// Stable is append-only by decision — a released artifact is never deleted, so
// that a rollback target cannot vanish. Immutable tags are the companion
// guarantee, set at repository creation rather than here.
func DefaultStableECRLifecycle() ECRLifecyclePolicy {
	return ECRLifecyclePolicy{KeepLast: 0}
}

// ConfigureLifecycle applies the retention policy to an ECR repository.
func ConfigureLifecycle(
	c *pulumi.Context,
	logger *slog.Logger,
	awsProvider *aws.Provider,
	repository *ecr.Repository,
	resourceName string,
	policy ECRLifecyclePolicy,
) error {
	ctx := c.Context()

	if policy.KeepLast <= 0 {
		logger.InfoContext(ctx, "skipping lifecycle policy (append-only mode)",
			slog.String("repository", resourceName),
		)

		return nil
	}

	rules := []ecr.GetLifecyclePolicyDocumentRule{{
		Priority: 1,
		Description: stringPtr(fmt.Sprintf(
			"Keep the last %d images (any tag status)", policy.KeepLast)),
		Selection: ecr.GetLifecyclePolicyDocumentRuleSelection{
			// "any", not "tagged": a manifest list and its platform children
			// must age out together, and no pattern may gate the rule.
			TagStatus:   "any",
			CountType:   lifecycleCountType,
			CountNumber: policy.KeepLast,
		},
		Action: &ecr.GetLifecyclePolicyDocumentRuleAction{
			Type: lifecycleAction,
		},
	}}

	lifecyclePolicyDoc, err := ecr.GetLifecyclePolicyDocument(c, &ecr.GetLifecyclePolicyDocumentArgs{
		Rules: rules,
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return fmt.Errorf("generate lifecycle policy document: %w", err)
	}

	_, err = ecr.NewLifecyclePolicy(c, resourceName, &ecr.LifecyclePolicyArgs{
		Repository: repository.Name,
		Policy:     pulumi.String(lifecyclePolicyDoc.Json),
	}, pulumi.Provider(awsProvider))
	if err != nil {
		return fmt.Errorf("create lifecycle policy: %w", err)
	}

	logger.InfoContext(ctx, "lifecycle policy applied",
		slog.String("repository", resourceName),
		slog.Int("keep_last", policy.KeepLast),
	)

	return nil
}

// stringPtr returns a pointer to the given string
func stringPtr(s string) *string {
	return &s
}
