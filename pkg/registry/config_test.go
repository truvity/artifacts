package registry

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Force-delete lets Pulumi delete an ECR repository that still holds images,
// so it must never spread: it is set only on repositories marked retiring.
func TestForceDeleteIsOffForAnUnmarkedRepository(t *testing.T) {
	t.Parallel()

	config := &Config{Retiring: map[string][]string{"p": {"charts/b"}}}

	assert.True(t, config.ForceDelete("p", "charts/b"))
	assert.False(t, config.ForceDelete("p", "a"))
	assert.False(t, config.ForceDelete("q", "charts/b"))
	assert.False(t, (&Config{}).ForceDelete("p", "charts/b"))
}

func TestValidateRequiresARegionAndAProject(t *testing.T) {
	t.Parallel()

	assert.Error(t, (&Config{Projects: []ProjectConfig{{Name: "p"}}}).Validate(), "no region")
	assert.Error(t, (&Config{PrimaryRegion: "eu-central-1"}).Validate(), "no project")

	c := &Config{PrimaryRegion: "eu-central-1", Projects: []ProjectConfig{{Name: "p", Components: []string{"api"}}}}
	require.NoError(t, c.Validate())
	assert.Equal(t, []string{"eu-central-1"}, c.Regions)
	assert.Equal(t, []string{"api"}, c.Projects[0].ECR, "the deprecated components: key migrates to ecr:")
}

func TestRepositoryNamesArePathsUnderTheProject(t *testing.T) {
	t.Parallel()

	p := ProjectConfig{Name: "shop", ECR: []string{"api", "charts/api"}, S3: []string{"fn"}}

	assert.Equal(t, []string{"shop/api", "shop/charts/api"}, p.RepositoryNames())
	assert.Equal(t, []string{"shop/fn"}, p.S3ComponentNames())
}

func TestCompanyProjects(t *testing.T) {
	t.Parallel()

	c := &Config{Projects: []ProjectConfig{
		{Name: "tool"},
		{Name: "a1", Company: "acme"},
		{Name: "a2", Company: "acme"},
		{Name: "z1", Company: "zeta"},
	}}

	names := func(ps []*ProjectConfig) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}

		return out
	}

	assert.Equal(t, []string{"tool"}, names(CompanyProjects(c, "infra")))
	assert.Equal(t, []string{"a1", "a2"}, names(CompanyProjects(c, "acme")))
	assert.Empty(t, CompanyProjects(c, "nobody"))
	// A name that is also a project is a per-project stack, not a group.
	assert.Nil(t, CompanyProjects(c, "a1"))
}

// The extra reader subjects are trust-policy patterns: a wildcard-first one
// would trust every tag of the repository.
func TestExtraTagPatternsNameOneFamily(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"sdk-typescript/v*", "sdk/v1.*"} {
		assert.NoError(t, validateExtraTagPattern(ok), ok)
	}

	for _, bad := range []string{"", "*", "v*", "refs/tags/v*", "a b/v*", "*/v1"} {
		assert.Error(t, validateExtraTagPattern(bad), bad)
	}

	release := &ReleaseConfig{
		CodeArtifactReaders:         []string{"p"},
		CodeArtifactReaderExtraTags: map[string][]string{"q": {"sdk/v*"}},
	}
	assert.Error(t, validateCodeArtifactReaderExtraTags(release), "q is not a reader")

	release = &ReleaseConfig{
		CodeArtifactReaders:            []string{"p"},
		CodeArtifactReaderEnvironments: map[string][]string{"p": {"*"}},
	}
	assert.Error(t, validateCodeArtifactReaderEnvironments(release), "an environment is one literal name")
	assert.True(t, slices.Equal(release.CodeArtifactReaders, []string{"p"}))
}
