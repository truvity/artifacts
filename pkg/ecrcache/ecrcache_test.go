package ecrcache

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

	return args.Name + "-id", args.Inputs, nil
}

func (r *recorder) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

// The prefixes are the contract with the node role's grants: a prefix that
// goes missing here is an image that can no longer be pulled.
func TestPrefixes(t *testing.T) {
	assert.Equal(t, []string{"docker-hub", "github", "quay", "registry-k8s-io"}, Prefixes())
}

// Resource names are state identity. The credentialled upstreams carry a
// secret and a version; the anonymous ones only the rule.
func TestDeployDeclaresRulesAndSecrets(t *testing.T) {
	rec := &recorder{}

	var asked []string

	err := pulumi.RunErr(func(c *pulumi.Context) error {
		provider, err := aws.NewProvider(c, "aws", &aws.ProviderArgs{})
		if err != nil {
			return err
		}

		return Deploy(c, slog.New(slog.DiscardHandler), Options{
			Provider: provider,
			Credentials: func(field string) (string, error) {
				asked = append(asked, field)

				return "value-of-" + field, nil
			},
			LegacyTopLevel: true,
		})
	}, pulumi.WithMocks("test", "test", rec))
	require.NoError(t, err)

	sort.Strings(asked)
	assert.Equal(t, []string{
		"docker-hub-personal-access-token", "docker-hub-username",
		"github-personal-access-token", "github-username",
	}, asked)

	sort.Strings(rec.names)
	assert.Equal(t, []string{
		"aws:ecr/pullThroughCacheRule:PullThroughCacheRule ptc-rule-docker-hub",
		"aws:ecr/pullThroughCacheRule:PullThroughCacheRule ptc-rule-github",
		"aws:ecr/pullThroughCacheRule:PullThroughCacheRule ptc-rule-quay",
		"aws:ecr/pullThroughCacheRule:PullThroughCacheRule ptc-rule-registry-k8s-io",
		"aws:secretsmanager/secret:Secret ptc-secret-docker-hub",
		"aws:secretsmanager/secret:Secret ptc-secret-github",
		"aws:secretsmanager/secretVersion:SecretVersion ptc-secret-version-docker-hub",
		"aws:secretsmanager/secretVersion:SecretVersion ptc-secret-version-github",
		"pulumi:providers:aws aws",
		"truvity:k8s/aws:PullThroughCache ecr-cache",
	}, rec.names)
}

func TestDeployRefusesWithoutAProvider(t *testing.T) {
	err := pulumi.RunErr(func(c *pulumi.Context) error {
		return Deploy(c, slog.New(slog.DiscardHandler), Options{})
	}, pulumi.WithMocks("test", "test", &recorder{}))
	require.Error(t, err)
}
