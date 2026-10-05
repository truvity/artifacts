// Package ecrcache is the standard set of ECR pull-through cache rules for
// public upstream registries. It is a per-account facility: the first pull
// lands the image in a local repository under the rule's prefix; later pulls
// are same-region, unthrottled and free of the upstream's per-IP rate limits.
// A cluster pulls through it (the kind table, docs/kinds.md), so a private
// cluster never depends on an upstream being reachable or generous.
//
// The mechanism is github.com/truvity/k8s/pkg/aws/pullthroughcache; this
// package adds the table of upstreams every account carries and the
// credential wiring. It holds no credential: the caller supplies a reader
// that answers for a field name.
//
// AWS constraints (the pull-through-cache-creating-secret page):
//   - docker.io and ghcr.io REQUIRE credentials even for public images,
//     stored in Secrets Manager with the MANDATORY name prefix
//     "ecr-pullthroughcache/", same account and region as the rule, default
//     aws/secretsmanager KMS key only (no CMK). quay.io and registry.k8s.io
//     are credential-free.
//   - The secret payload is exactly {"username": ..., "accessToken": ...}.
//   - ECR creates its service-linked role automatically on rule creation.
//
// Pulling principals also need ecr:BatchImportUpstreamImage and
// ecr:CreateRepository on the prefix repositories: granted where the puller's
// role is defined (the node role of the cluster), on exactly Prefixes().
package ecrcache

import (
	"fmt"
	"log/slog"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/truvity/k8s/pkg/aws/pullthroughcache"
)

// ComponentName is the Pulumi name of the pull-through cache component, part
// of every child's URN.
const ComponentName = "ecr-cache"

// Upstream is one cached registry and where its credentials come from.
type Upstream struct {
	// Prefix is the local repository namespace: pulling
	// {account}.dkr.ecr.{region}.amazonaws.com/github/actions/actions-runner
	// caches ghcr.io/actions/actions-runner.
	Prefix string
	// URL is the upstream registry host.
	URL string
	// UsernameField and TokenField name the credential fields the caller's
	// reader is asked for. Both empty means the upstream is pulled
	// anonymously (no secret, no credential on the rule).
	UsernameField string
	TokenField    string
}

// Upstreams is the standard set of pull-through cache rules.
var Upstreams = []Upstream{
	{
		Prefix:        "docker-hub",
		URL:           "registry-1.docker.io",
		UsernameField: "docker-hub-username",
		TokenField:    "docker-hub-personal-access-token",
	},
	{
		Prefix:        "github",
		URL:           "ghcr.io",
		UsernameField: "github-username",
		TokenField:    "github-personal-access-token",
	},
	{
		Prefix: "quay",
		URL:    "quay.io",
	},
	{
		Prefix: "registry-k8s-io",
		URL:    "registry.k8s.io",
	},
}

// Prefixes returns the local repository namespaces of every pull-through
// cache rule, in table order. The node role of a cluster is granted
// ecr:BatchImportUpstreamImage and ecr:CreateRepository on exactly these.
func Prefixes() []string {
	prefixes := make([]string, len(Upstreams))
	for i, u := range Upstreams {
		prefixes[i] = u.Prefix
	}

	return prefixes
}

// CredentialReader returns the value of a credential field (a name from
// Upstream.UsernameField or TokenField). It is called at program time, once
// per field, for the upstreams that need credentials.
type CredentialReader func(field string) (string, error)

// Options configures Deploy.
type Options struct {
	// Provider is the AWS provider of the account and region the rules live in.
	Provider *aws.Provider

	// Credentials reads the credential fields of the upstreams that need one.
	Credentials CredentialReader

	// LegacyTopLevel adopts rules, secrets and versions that were registered
	// directly under the stack before they were wrapped in the component: each
	// child carries an alias from there, so state adopts it with no replace.
	LegacyTopLevel bool
}

// Deploy declares the pull-through cache rules (Upstreams) in the account of
// opts.Provider, as one pullthroughcache component named ComponentName.
func Deploy(c *pulumi.Context, logger *slog.Logger, opts Options) error {
	if opts.Provider == nil {
		return fmt.Errorf("ecrcache: Provider is required")
	}

	args := &pullthroughcache.Args{
		Credentials:    map[string]pullthroughcache.Credentials{},
		LegacyTopLevel: opts.LegacyTopLevel,
	}

	for _, upstream := range Upstreams {
		args.Upstreams = append(args.Upstreams, pullthroughcache.Upstream{
			Prefix:           upstream.Prefix,
			RegistryURL:      upstream.URL,
			NeedsCredentials: upstream.UsernameField != "",
		})

		if upstream.UsernameField == "" {
			continue
		}

		if opts.Credentials == nil {
			return fmt.Errorf("ecrcache: Credentials is required: upstream %s needs credentials", upstream.Prefix)
		}

		username, err := opts.Credentials(upstream.UsernameField)
		if err != nil {
			return fmt.Errorf("read %s: %w", upstream.UsernameField, err)
		}

		token, err := opts.Credentials(upstream.TokenField)
		if err != nil {
			return fmt.Errorf("read %s: %w", upstream.TokenField, err)
		}

		args.Credentials[upstream.Prefix] = pullthroughcache.Credentials{
			Username: pulumi.String(username),
			Token:    pulumi.String(token),
		}
	}

	if _, err := pullthroughcache.NewPullThroughCache(c, ComponentName, args, pulumi.Providers(opts.Provider)); err != nil {
		return fmt.Errorf("create pull-through cache: %w", err)
	}

	for _, upstream := range Upstreams {
		logger.InfoContext(c.Context(), "pull-through cache rule deployed",
			slog.String("prefix", upstream.Prefix),
			slog.String("upstream", upstream.URL),
		)
	}

	return nil
}
