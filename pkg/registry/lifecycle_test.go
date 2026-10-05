package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The retention rule must never be keyed to a tag pattern again.
//
// The previous policy matched `v*-dev.*` while every image was tagged
// `1.21.3-<sha>-nightly`, so it deleted nothing for months and reported no
// error. These tests fix the two properties that failure violated: snapshot
// registries prune by COUNT, and release registries prune not at all.
func TestPreviewKeepsABoundedCount(t *testing.T) {
	t.Parallel()

	p := DefaultPreviewECRLifecycle()

	require.Positive(t, p.KeepLast, "snapshot retention must be bounded, or new-devel grows forever")
	assert.Equal(t, 750, p.KeepLast)
}

func TestStableIsAppendOnly(t *testing.T) {
	t.Parallel()

	// Zero is the documented signal for "write no policy at all", which is
	// what keeps a released artifact available as a rollback target.
	assert.Zero(t, DefaultStableECRLifecycle().KeepLast)
}

func TestKindSelectsTheRightRetention(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DefaultStableECRLifecycle(), ECRLifecyclePolicyForKind(RepositoryKindStable))
	assert.Equal(t, DefaultPreviewECRLifecycle(), ECRLifecyclePolicyForKind(RepositoryKindPreview))

	// An unrecognized kind must fall to the BOUNDED policy. Defaulting to
	// append-only would make a typo in a project row silently disable
	// retention — the failure this whole change exists to prevent.
	assert.Equal(t, DefaultPreviewECRLifecycle(), ECRLifecyclePolicyForKind(RepositoryKind("nonsense")))
}
