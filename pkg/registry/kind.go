package registry

type (
	// RepositoryKind is the tier of a registry: preview or stable.
	RepositoryKind string

	// ReaderAccountIDs holds AWS account IDs for reader account determination,
	// by the role the account plays. Each field is a slice so several accounts
	// can play one role (for instance during a migration). The module knows no
	// account: every ID comes from the caller.
	ReaderAccountIDs struct {
		Root    []string // Root account IDs (for ssosync Lambda)
		Kernel  []string // Kernel account IDs
		Sandbox []string // Sandbox account IDs
		Devel   []string // Devel account IDs
		Stage   []string // Stage account IDs
		Prod    []string // Prod account IDs
	}
)

const (
	// RepositoryKindPreview represents preview repositories (snapshot builds):
	// mutable tags, bounded history.
	RepositoryKindPreview RepositoryKind = "preview"

	// RepositoryKindStable represents stable repositories (pre-release and
	// release builds): immutable tags, append-only.
	RepositoryKindStable RepositoryKind = "stable"
)

// TagMutabilityForKind returns the appropriate tag mutability setting for a repository kind
func TagMutabilityForKind(kind RepositoryKind) string {
	switch kind {
	case RepositoryKindPreview:
		// Preview: mutable for rapid iteration
		return "MUTABLE"
	case RepositoryKindStable:
		// Stable: immutable with exceptions for latest* tags
		// Version tags (v1.2.3-amd64) remain immutable for audit trail
		// Latest tags (latest, latest-amd64, latest-arm64) are mutable
		return "IMMUTABLE_WITH_EXCLUSION"
	default:
		return "MUTABLE"
	}
}

// TagMutabilityExclusionFiltersForKind returns tag patterns that should be mutable
// even in immutable repositories. Only applies when TagMutabilityForKind returns IMMUTABLE_WITH_EXCLUSION
func TagMutabilityExclusionFiltersForKind(kind RepositoryKind) []string {
	switch kind {
	case RepositoryKindStable:
		// Allow latest* tags to be updated (latest, latest-amd64, latest-arm64)
		// These tags point to the current release and should be updatable
		return []string{"latest*"}
	case RepositoryKindPreview:
		// Preview repositories are fully mutable, no exclusions needed
		return nil
	default:
		return nil
	}
}

// ECRLifecyclePolicyForKind returns the appropriate ECR lifecycle policy for a repository kind
func ECRLifecyclePolicyForKind(kind RepositoryKind) ECRLifecyclePolicy {
	switch kind {
	case RepositoryKindPreview:
		return DefaultPreviewECRLifecycle()
	case RepositoryKindStable:
		return DefaultStableECRLifecycle()
	default:
		return DefaultPreviewECRLifecycle()
	}
}

// S3LifecyclePolicyForKind returns the appropriate S3 lifecycle policy for a repository kind
func S3LifecyclePolicyForKind(kind RepositoryKind) S3LifecyclePolicy {
	switch kind {
	case RepositoryKindPreview:
		return DefaultPreviewS3Lifecycle()
	case RepositoryKindStable:
		return DefaultStableS3Lifecycle()
	default:
		return DefaultPreviewS3Lifecycle()
	}
}

// DefaultReaderAccountsForKind returns default reader account IDs based on repository kind
// Preview repositories: kernel + sandbox + devel accounts
// Stable repositories: root + kernel + sandbox + devel + stage + prod accounts
// Note: Kernel is always included because anchor-kernel Lambda runs in kernel account
// Note: Root is included in stable because ssosync Lambda runs in root account
func DefaultReaderAccountsForKind(kind RepositoryKind, accounts ReaderAccountIDs) []string {
	var result []string

	appendAll := func(ids []string) {
		for _, id := range ids {
			if id != "" {
				result = append(result, id)
			}
		}
	}

	switch kind {
	case RepositoryKindPreview:
		appendAll(accounts.Kernel)
		appendAll(accounts.Sandbox)
		appendAll(accounts.Devel)
	case RepositoryKindStable:
		appendAll(accounts.Root)
		appendAll(accounts.Kernel)
		appendAll(accounts.Sandbox)
		appendAll(accounts.Devel)
		appendAll(accounts.Stage)
		appendAll(accounts.Prod)
	}

	return result
}
