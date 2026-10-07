// Package codeartifact deploys a CodeArtifact domain and its repositories: the
// private package registry of the kind table (docs/kinds.md) for npm, Maven and
// Python packages.
//
// The caller states the domain, the repositories, each repository's upstreams
// (other repositories of the same domain, in lookup order) and at most one
// external connection (a public registry such as public:npmjs). The domain is
// encrypted with the key the caller names, or with CodeArtifact's AWS-managed
// key when none is named.
//
// A domain and repositories that already exist are adopted with Config.Adopt:
// every resource is registered with the import option and its ARN, so the first
// deploy imports them and changes nothing, and a later one is a no-op. Import
// refuses inputs that differ from the live resource rather than replacing it.
//
// The domain and the repositories are protected unless Config.Protect points at
// false: deleting a repository deletes its packages, and a domain cannot be
// deleted while it has repositories.
//
// The resources are registered directly under the caller's stack, not under a
// component, like the rest of this module (docs/contract.md).
package codeartifact

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/codeartifact"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Repository is one repository of the domain.
type Repository struct {
	// Name is the repository's name. Required, unique in the domain.
	Name string
	// Description is the repository's description. Empty sets none.
	Description string
	// Upstreams are repositories of the same domain this one reads through,
	// in lookup order. Each must be declared before this one in
	// Config.Repositories, so a cycle cannot be written.
	Upstreams []string
	// ExternalConnection is the public registry the repository caches, for
	// example public:npmjs. Empty: none. CodeArtifact admits one per
	// repository.
	ExternalConnection string
}

// Adoption names the account and region of a domain that already exists, so
// its resources are imported rather than created.
type Adoption struct {
	AccountID string
	Region    string
}

// Config configures Deploy.
type Config struct {
	// Provider is the AWS provider of the domain's account and region.
	// Required.
	Provider *aws.Provider
	// Domain is the domain's name. Required.
	Domain string
	// EncryptionKeyARN is the KMS key the domain's assets are encrypted with.
	// Empty: CodeArtifact's AWS-managed key. A domain's key cannot be changed
	// once it exists.
	EncryptionKeyARN string
	// Repositories are the domain's repositories, upstreams first.
	Repositories []Repository
	// Tags are set on the domain and every repository. Nil sets none.
	Tags map[string]string
	// Adopt imports an existing domain and its repositories. Nil creates
	// them.
	Adopt *Adoption
	// Protect marks the domain and the repositories protected. Nil (the
	// default) protects them.
	Protect *bool
}

func (c *Config) protect() bool { return c.Protect == nil || *c.Protect }

// Result is what Deploy registered.
type Result struct {
	// Domain is the domain.
	Domain *codeartifact.Domain
	// Repositories are the repositories by name.
	Repositories map[string]*codeartifact.Repository
}

// DomainResourceName and RepositoryResourceName are the Pulumi names of the
// domain and of a repository. Names are API (docs/contract.md).
func DomainResourceName(domain string) string { return "codeartifact-" + domain }

// RepositoryResourceName is the Pulumi name of a repository of domain.
func RepositoryResourceName(domain, repository string) string {
	return "codeartifact-" + domain + "-" + repository
}

// DomainARN and RepositoryARN are the ARNs an adopted domain and repository
// are imported by.
func DomainARN(a Adoption, domain string) string {
	return fmt.Sprintf("arn:aws:codeartifact:%s:%s:domain/%s", a.Region, a.AccountID, domain)
}

// RepositoryARN is the ARN a repository of domain is imported by.
func RepositoryARN(a Adoption, domain, repository string) string {
	return fmt.Sprintf("arn:aws:codeartifact:%s:%s:repository/%s/%s", a.Region, a.AccountID, domain, repository)
}

var (
	// CodeArtifact's documented name rules: a domain is 2 to 50 lowercase
	// letters, digits and hyphens starting with a letter; a repository is 2
	// to 100 letters, digits, '.', '-' and '_' starting with a letter or
	// digit.
	domainName     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,49}$`)
	repositoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,99}$`)
	accountID      = regexp.MustCompile(`^[0-9]{12}$`)
)

// Validate reports every problem with the configuration at once, or returns
// nil.
func (c *Config) Validate() error {
	var errs []error

	if c.Provider == nil {
		errs = append(errs, errors.New("codeartifact: Provider is nil"))
	}

	if !domainName.MatchString(c.Domain) {
		errs = append(errs, fmt.Errorf("codeartifact: Domain %q is not a CodeArtifact domain name", c.Domain))
	}

	if a := c.Adopt; a != nil {
		if !accountID.MatchString(a.AccountID) {
			errs = append(errs, fmt.Errorf("codeartifact: Adopt.AccountID %q is not an account ID", a.AccountID))
		}

		if a.Region == "" {
			errs = append(errs, errors.New("codeartifact: Adopt.Region is empty"))
		}
	}

	declared := map[string]bool{}

	for i, r := range c.Repositories {
		switch {
		case !repositoryName.MatchString(r.Name):
			errs = append(errs, fmt.Errorf("codeartifact: Repositories[%d].Name %q is not a CodeArtifact repository name", i, r.Name))
		case declared[r.Name]:
			errs = append(errs, fmt.Errorf("codeartifact: Repositories[%d]: duplicate name %q", i, r.Name))
		}

		seen := map[string]bool{}

		for _, u := range r.Upstreams {
			switch {
			case !declared[u]:
				errs = append(errs, fmt.Errorf("codeartifact: repository %q: upstream %q is not declared before it", r.Name, u))
			case seen[u]:
				errs = append(errs, fmt.Errorf("codeartifact: repository %q: upstream %q is listed twice", r.Name, u))
			}

			seen[u] = true
		}

		if e := r.ExternalConnection; e != "" && !strings.HasPrefix(e, "public:") {
			errs = append(errs, fmt.Errorf("codeartifact: repository %q: external connection %q is not a public:<registry> name", r.Name, e))
		}

		declared[r.Name] = true
	}

	return errors.Join(errs...)
}

// Deploy registers the domain and its repositories. It returns an error,
// registering nothing, when Validate does.
func Deploy(c *pulumi.Context, logger *slog.Logger, cfg Config) (*Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	opts := func(importID string, extra ...pulumi.ResourceOption) []pulumi.ResourceOption {
		out := append([]pulumi.ResourceOption{pulumi.Provider(cfg.Provider)}, extra...)

		if cfg.Adopt != nil {
			out = append(out, pulumi.Import(pulumi.ID(importID)))
		}

		if cfg.protect() {
			out = append(out, pulumi.Protect(true))
		}

		return out
	}

	var tags pulumi.StringMapInput
	if len(cfg.Tags) > 0 {
		tags = pulumi.ToStringMap(cfg.Tags)
	}

	var adopt Adoption
	if cfg.Adopt != nil {
		adopt = *cfg.Adopt
	}

	domainArgs := &codeartifact.DomainArgs{
		Domain: pulumi.String(cfg.Domain),
		Tags:   tags,
	}
	if cfg.EncryptionKeyARN != "" {
		domainArgs.EncryptionKey = pulumi.String(cfg.EncryptionKeyARN)
	}

	domain, err := codeartifact.NewDomain(c, DomainResourceName(cfg.Domain), domainArgs, opts(DomainARN(adopt, cfg.Domain))...)
	if err != nil {
		return nil, fmt.Errorf("codeartifact domain %s: %w", cfg.Domain, err)
	}

	res := &Result{Domain: domain, Repositories: map[string]*codeartifact.Repository{}}

	for _, r := range cfg.Repositories {
		// DomainOwner is left to the provider: it is the domain's account,
		// which the provider reads, and stating it would only be a second
		// copy of that fact.
		args := &codeartifact.RepositoryArgs{
			Domain:     domain.Domain,
			Repository: pulumi.String(r.Name),
			Tags:       tags,
		}

		if r.Description != "" {
			args.Description = pulumi.String(r.Description)
		}

		deps := []pulumi.Resource{domain}

		if len(r.Upstreams) > 0 {
			upstreams := codeartifact.RepositoryUpstreamArray{}

			for _, u := range r.Upstreams {
				upstreams = append(upstreams, codeartifact.RepositoryUpstreamArgs{RepositoryName: pulumi.String(u)})
				deps = append(deps, res.Repositories[u])
			}

			args.Upstreams = upstreams
		}

		if r.ExternalConnection != "" {
			args.ExternalConnections = &codeartifact.RepositoryExternalConnectionsArgs{
				ExternalConnectionName: pulumi.String(r.ExternalConnection),
			}
		}

		repo, err := codeartifact.NewRepository(c, RepositoryResourceName(cfg.Domain, r.Name), args,
			opts(RepositoryARN(adopt, cfg.Domain, r.Name), pulumi.DependsOn(deps))...)
		if err != nil {
			return nil, fmt.Errorf("codeartifact repository %s/%s: %w", cfg.Domain, r.Name, err)
		}

		res.Repositories[r.Name] = repo

		if logger != nil {
			logger.InfoContext(c.Context(), "codeartifact repository declared",
				slog.String("domain", cfg.Domain), slog.String("repository", r.Name), slog.Bool("adopt", cfg.Adopt != nil))
		}
	}

	return res, nil
}
