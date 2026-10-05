package registry

import (
	"log/slog"
	"sort"
	"sync"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	mu    sync.Mutex
	names []string
}

func (r *recorder) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	r.mu.Lock()
	r.names = append(r.names, args.TypeToken+" "+args.Name)
	r.mu.Unlock()

	state := args.Inputs.Copy()
	state["arn"] = resource.NewProperty("arn:mock:" + args.Name)

	return args.Name + "-id", state, nil
}

func (r *recorder) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func run(t *testing.T, program func(c *pulumi.Context, provider *aws.Provider) error) []string {
	t.Helper()

	rec := &recorder{}

	require.NoError(t, pulumi.RunErr(func(c *pulumi.Context) error {
		provider, err := aws.NewProvider(c, "aws", &aws.ProviderArgs{})
		if err != nil {
			return err
		}

		return program(c, provider)
	}, pulumi.WithMocks("test", "test", rec)))

	sort.Strings(rec.names)

	return rec.names
}

// Resource names are state identity: a project's repository is named
// "<project>/<component>/<region>", its lifecycle and policy beside it. A
// rename is a replace of a repository that holds images.
func TestDeployECRResourceNames(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	got := run(t, func(c *pulumi.Context, provider *aws.Provider) error {
		return DeployECR(c, logger, DeployECRConfig{
			Project:        &ProjectConfig{Name: "shop", ECR: []string{"api", "charts/api"}},
			Region:         "eu-central-1",
			Kind:           RepositoryKindPreview,
			AWSProvider:    provider,
			AccountID:      "111122223333",
			ReaderAccounts: ReaderAccountIDs{Devel: []string{"444455556666"}},
		})
	})

	assert.Equal(t, []string{
		"aws:ecr/lifecyclePolicy:LifecyclePolicy shop/api/lifecycle/eu-central-1",
		"aws:ecr/lifecyclePolicy:LifecyclePolicy shop/charts/api/lifecycle/eu-central-1",
		"aws:ecr/repository:Repository shop/api/eu-central-1",
		"aws:ecr/repository:Repository shop/charts/api/eu-central-1",
		"aws:ecr/repositoryPolicy:RepositoryPolicy shop/api/policy/eu-central-1",
		"aws:ecr/repositoryPolicy:RepositoryPolicy shop/charts/api/policy/eu-central-1",
		"pulumi:providers:aws aws",
	}, got)
}

// The stable tier is append-only: no lifecycle policy is written at all.
func TestDeployECRStableHasNoLifecycle(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	got := run(t, func(c *pulumi.Context, provider *aws.Provider) error {
		return DeployECR(c, logger, DeployECRConfig{
			Project:     &ProjectConfig{Name: "shop", ECR: []string{"api"}},
			Region:      "eu-central-1",
			Kind:        RepositoryKindStable,
			AWSProvider: provider,
			AccountID:   "111122223333",
		})
	})

	for _, name := range got {
		assert.NotContains(t, name, "lifecyclePolicy")
	}
}

func TestDeployReleaseStableDeploysOneRolePerProjectAndTheCache(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	got := run(t, func(c *pulumi.Context, provider *aws.Provider) error {
		return DeployRelease(c, logger, ReleaseStackConfig{
			Kind: RepositoryKindStable,
			Config: &Config{
				PrimaryRegion: "eu-central-1",
				Projects:      []ProjectConfig{{Name: "shop", ECR: []string{"api"}}, {Name: "tool"}},
				Release:       &ReleaseConfig{SourceRepo: "acme/mono", SubjectPrefix: "repo:acme/mono"},
			},
			AccountID:                "111122223333",
			AWSProvider:              provider,
			OIDCProviderResourceName: "oidc",
		})
	})

	assert.Equal(t, []string{
		"aws:ecr/lifecyclePolicy:LifecyclePolicy ci-buildkit-cache-lifecycle",
		"aws:ecr/repository:Repository ci-buildkit-cache",
		"aws:iam/openIdConnectProvider:OpenIdConnectProvider oidc",
		"aws:iam/role:Role release-role-shop",
		"aws:iam/rolePolicy:RolePolicy release-policy-shop",
		"pulumi:providers:aws aws",
	}, got, "a project with no repositories gets no role")
}

// The preview tier has no publish identities: snapshot pushes ride the CI
// pool's identity.
func TestDeployReleasePreviewHasNoRoles(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	got := run(t, func(c *pulumi.Context, provider *aws.Provider) error {
		return DeployRelease(c, logger, ReleaseStackConfig{
			Kind: RepositoryKindPreview,
			Config: &Config{
				PrimaryRegion: "eu-central-1",
				Projects:      []ProjectConfig{{Name: "shop", ECR: []string{"api"}}},
				Release:       &ReleaseConfig{SourceRepo: "acme/mono", SubjectPrefix: "repo:acme/mono"},
			},
			AccountID:                "111122223333",
			AWSProvider:              provider,
			OIDCProviderResourceName: "oidc",
			BuildkitCache:            true,
		})
	})

	assert.Equal(t, []string{
		"aws:ecr/lifecyclePolicy:LifecyclePolicy ci-buildkit-cache-lifecycle",
		"aws:ecr/repository:Repository ci-buildkit-cache",
		"aws:iam/openIdConnectProvider:OpenIdConnectProvider oidc",
		"pulumi:providers:aws aws",
	}, got)
}

func TestCodeArtifactReadersNeedAPolicyAndADeclaredProject(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	cfg := func(readers ...string) *Config {
		return &Config{
			PrimaryRegion: "eu-central-1",
			Projects:      []ProjectConfig{{Name: "shop", ECR: []string{"api"}}},
			Release: &ReleaseConfig{
				SourceRepo: "acme/mono", SubjectPrefix: "repo:acme/mono",
				CodeArtifactReaders: readers,
			},
		}
	}

	deploy := func(config *Config, opts CodeArtifactReaderOptions) error {
		return pulumi.RunErr(func(c *pulumi.Context) error {
			provider, err := aws.NewProvider(c, "aws", &aws.ProviderArgs{})
			if err != nil {
				return err
			}

			oidc := pulumi.String("arn:mock:oidc").ToStringOutput()

			return DeployCodeArtifactReaderRoles(c, logger, provider, oidc, "111122223333", config, opts)
		}, pulumi.WithMocks("test", "test", &recorder{}))
	}

	assert.Error(t, deploy(cfg("shop"), CodeArtifactReaderOptions{}), "no read policy")
	assert.Error(t, deploy(cfg("ghost"), CodeArtifactReaderOptions{ReadPolicyARN: "arn:mock:policy"}), "undeclared project")
	assert.NoError(t, deploy(cfg("shop"), CodeArtifactReaderOptions{ReadPolicyARN: "arn:mock:policy"}))
}
