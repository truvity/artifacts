package registry

import (
	"fmt"
	"slices"
	"strings"
)

type (
	// Config holds the configuration of a registry: which projects own which
	// repositories, and who may publish them. The module does not load it;
	// the caller builds it (typically from a YAML file it owns, the yaml tags
	// are there for that).
	Config struct {
		// PrimaryRegion is the AWS region of the ECR repositories
		PrimaryRegion string `yaml:"primary_region"`
		// Regions is the list of all AWS regions for S3 buckets and SSM parameters
		Regions []string `yaml:"regions"`
		// Region is deprecated, kept for backward compatibility
		Region   string          `yaml:"region"`
		Projects []ProjectConfig `yaml:"projects"`
		// Release configures the publish identities (release-{project}
		// roles) deployed with the registry account's identities.
		Release *ReleaseConfig `yaml:"release"`
		// Retiring maps a project to its ECR components being removed: ONLY
		// these repositories get force-delete. Kept here, not on
		// ProjectConfig, which is copied by value in many loops.
		Retiring map[string][]string `yaml:"-"`
	}

	// ReleaseConfig declares where release workflows run and who may
	// publish by hand.
	ReleaseConfig struct {
		// SourceRepo is the GitHub repository ("owner/repo") whose
		// tag-driven workflows may assume release roles. Subjects pin to
		// refs/tags/{project}/v* (the monorepo tag convention); a project
		// that owns its repository overrides both halves (Overrides).
		SourceRepo string `yaml:"source_repo"`

		// SubjectPrefix is the head of the OIDC subject GitHub signs for
		// SourceRepo, verbatim -- "repo:owner/repo" for a repository
		// GitHub still signs by name, "repo:{owner}@{id}/{repo}@{id}" for
		// one created after its immutable-identifier rollout.
		//
		// Stated rather than composed, for every repository including the
		// name-shaped ones. Composing it worked only by accident of
		// repository age, and the accident expired: a release was
		// refused with a bare "Not authorized to perform
		// sts:AssumeRoleWithWebIdentity", which names neither the claim
		// nor the reason. CompareSubjects checks the stated value against
		// the live one, so the check can happen at review time and not at
		// tag time.
		SubjectPrefix string `yaml:"subject_prefix"`

		// HumanSSOPermissionSet names the AWS SSO permission set whose
		// provisioned roles may assume release roles interactively (the
		// manual/break-glass publish path). Matched by aws:PrincipalArn
		// pattern because AWSReservedSSO role names carry random suffixes
		// that change on re-provision.
		HumanSSOPermissionSet string `yaml:"human_sso_permission_set"`

		// Overrides names the projects that do NOT publish from
		// SourceRepo. A project extracted into its own repository keeps
		// its release role and its ECR repositories; only the identity
		// allowed to assume the role moves, so this is a trust edit
		// rather than a re-provision.
		//
		// Keyed by project name; absent means SourceRepo with the
		// estate's {project}/v* tag.
		Overrides map[string]ReleaseOverride `yaml:"overrides,omitempty"`

		// CodeArtifactReaders names the projects whose RELEASE builds read
		// the private npm registry, which lives in the account that deploys
		// the readers. Each gets a read-only role there, trusting exactly
		// the subject its stable publish role trusts.
		//
		// A project needs this only when its release runs somewhere with
		// no ambient identity — a GitHub-hosted runner. In-cluster runners
		// mint the token with the pool's identity and need no role.
		CodeArtifactReaders []string `yaml:"codeartifact_readers,omitempty"`

		// CodeArtifactReaderExtraTags widens ONLY the read-only
		// CodeArtifact reader role (not the stable ECR-push role) to trust
		// additional tag refs, keyed by project name. Each value is a list
		// of ref suffixes after refs/tags/, e.g. "sdk-typescript/v*".
		//
		// Why it exists: a repository can cut more than one kind of
		// release tag. A product may tag its backend images `v*` (the
		// publish role's identity) AND its SDKs `sdk-typescript/v*` /
		// `sdk-java/v*`. The SDK publish workflow runs on a GitHub-hosted
		// runner and must read the private npm packages to build the SDK,
		// but its OIDC subject is
		// `...:ref:refs/tags/sdk-typescript/v*`, which the base `v*`
		// subject does not match — so without this the SDK release cannot
		// obtain the read token and fails at AssumeRoleWithWebIdentity.
		//
		// This widens READ only. The reader role grants npm read and
		// nothing else (see DeployCodeArtifactReaderRoles); the stable
		// publish role that pushes images to ECR keeps its single `v*`
		// subject, so an SDK tag still cannot push a backend image.
		CodeArtifactReaderExtraTags map[string][]string `yaml:"codeartifact_reader_extra_tags,omitempty"`

		// CodeArtifactReaderEnvironments widens ONLY the read-only reader
		// role to trust a job that runs under a named GitHub environment,
		// keyed by project name. Each value is a list of environment
		// names, e.g. "npm-publish".
		//
		// Why it exists: a job that references a GitHub environment is
		// signed with the subject `...:environment:<name>` INSTEAD of the
		// tag-ref subject, so a tag pattern above cannot match it. A
		// TypeScript SDK publish runs under an environment because npm
		// trusted publishing filters on repository, workflow file and
		// environment -- never on a git ref -- and the environment's
		// deployment rule (tags `sdk-typescript/v*` only, set on the
		// repository) is what keeps the ref gate: GitHub refuses to start
		// the job from any other ref, so the environment subject is no
		// wider than the tag subject it stands in for.
		//
		// READ only, as the extra tags: the stable publish role keeps its
		// single `v*` subject, so an environment job still cannot push a
		// backend image.
		CodeArtifactReaderEnvironments map[string][]string `yaml:"codeartifact_reader_environments,omitempty"`
	}

	// ReleaseOverride redirects one project's publish identity.
	ReleaseOverride struct {
		// SourceRepo is the "owner/repo" whose tag workflows may assume
		// the role. Empty keeps the shared one.
		SourceRepo string `yaml:"source_repo,omitempty"`

		// TagPattern is the ref suffix after refs/tags/, e.g. "v*".
		//
		// A project sharing a monorepo needs the {project}/ prefix to
		// tell its tags from its siblings'; one that owns its repository
		// does not, and carrying the prefix there would mean tagging
		// "app/v0.33.0" in a repository containing only app. Empty keeps
		// "{project}/v*".
		TagPattern string `yaml:"tag_pattern,omitempty"`

		// SubjectPrefix replaces the "repo:{owner}/{repo}" head of the
		// OIDC subject.
		//
		// GitHub does not always sign the name form. Repositories created
		// after its immutable-identifier rollout emit
		// "repo:{owner}@{owner_id}/{repo}@{repo_id}" instead, and a
		// trust policy written against the names then matches NOTHING —
		// the release fails at AssumeRoleWithWebIdentity with a bare
		// "Not authorized", which names neither the claim nor the reason
		// (a release, 2026-09-06; the subject came from CloudTrail,
		// which records it verbatim).
		//
		// Pinning the ids is the point of the feature, not a fragility:
		// they survive a rename, whereas the name form silently follows
		// whatever repository holds the name today. Read the exact value
		// per repository -- do not assemble it by hand:
		//
		//	gh api /repos/{owner}/{repo}/actions/oidc/customization/sub \
		//	  --jq .sub_claim_prefix
		//
		// Empty derives "repo:{SourceRepo or release.source_repo}".
		SubjectPrefix string `yaml:"subject_prefix,omitempty"`
	}

	// ProjectConfig defines a project and its components
	ProjectConfig struct {
		Name       string   `yaml:"name"`
		ECR        []string `yaml:"ecr"`                  // ECR components (Docker images)
		S3         []string `yaml:"s3"`                   // S3 components (ZIP archives for Lambda)
		Components []string `yaml:"components,omitempty"` // Deprecated: use ecr/s3 instead
		// Company is the owning company ("" = platform infrastructure).
		// Groups projects into per-company registry stacks.
		Company string `yaml:"company,omitempty"`
	}
)

// Validate checks the configuration is valid.
func (c *Config) Validate() error {
	// Backward compatibility: if old region field is set but new fields are not, migrate
	if c.PrimaryRegion == "" && c.Region != "" {
		c.PrimaryRegion = c.Region
	}
	if len(c.Regions) == 0 && c.Region != "" {
		c.Regions = []string{c.Region}
	}
	if c.PrimaryRegion == "" {
		return fmt.Errorf("primary_region is required")
	}
	if len(c.Regions) == 0 {
		c.Regions = []string{c.PrimaryRegion}
	}

	if len(c.Projects) == 0 {
		return fmt.Errorf("at least one project is required")
	}

	for i := range c.Projects {
		project := &c.Projects[i]
		if project.Name == "" {
			return fmt.Errorf("project name is required")
		}
		// Migrate old components: field to ecr: if present
		if len(project.Components) > 0 && len(project.ECR) == 0 {
			project.ECR = project.Components
			project.Components = nil
		}
	}

	if c.Release != nil {
		if err := validateCodeArtifactReaderExtraTags(c.Release); err != nil {
			return err
		}
		if err := validateCodeArtifactReaderEnvironments(c.Release); err != nil {
			return err
		}
	}

	return nil
}

// validateCodeArtifactReaderExtraTags keeps the reader-role widening from
// growing by accident. The extras are trust-policy subjects, so a typo is
// not cosmetic: a key that is not a reader would be silently ignored, and a
// pattern such as "*" would trust every tag in the repository.
func validateCodeArtifactReaderExtraTags(release *ReleaseConfig) error {
	readers := make(map[string]bool, len(release.CodeArtifactReaders))
	for _, r := range release.CodeArtifactReaders {
		readers[r] = true
	}

	for project, patterns := range release.CodeArtifactReaderExtraTags {
		if !readers[project] {
			return fmt.Errorf("codeartifact_reader_extra_tags: %q is not in codeartifact_readers", project)
		}
		for _, pattern := range patterns {
			if err := validateExtraTagPattern(pattern); err != nil {
				return fmt.Errorf("codeartifact_reader_extra_tags[%s]: %w", project, err)
			}
		}
	}

	return nil
}

// validateExtraTagPattern accepts a ref suffix after refs/tags/ that starts
// with a literal prefix ending in "/" before any wildcard, e.g.
// "sdk-typescript/v*". That shape names one tag family and nothing wider.
func validateExtraTagPattern(pattern string) error {
	switch {
	case pattern == "":
		return fmt.Errorf("empty tag pattern")
	case strings.HasPrefix(pattern, "refs/"):
		return fmt.Errorf("tag pattern %q must be the part after refs/tags/", pattern)
	case strings.ContainsAny(pattern, " \t\n:"):
		return fmt.Errorf("tag pattern %q contains whitespace or ':'", pattern)
	}

	wildcard := strings.IndexAny(pattern, "*?")
	slash := strings.Index(pattern, "/")
	if slash <= 0 || (wildcard >= 0 && wildcard < slash) {
		return fmt.Errorf("tag pattern %q must start with a literal \"<family>/\" prefix before any wildcard", pattern)
	}

	return nil
}

// validateCodeArtifactReaderEnvironments holds the environment widening to
// the same rule as the tags: a declared reader, and one literal name per
// entry. An environment name is matched verbatim by GitHub, so a wildcard
// here would not name a family -- it would be a subject nothing signs, or
// with `*` alone, one that every environment of the repository signs.
func validateCodeArtifactReaderEnvironments(release *ReleaseConfig) error {
	readers := make(map[string]bool, len(release.CodeArtifactReaders))
	for _, r := range release.CodeArtifactReaders {
		readers[r] = true
	}

	for project, names := range release.CodeArtifactReaderEnvironments {
		if !readers[project] {
			return fmt.Errorf("codeartifact_reader_environments: %q is not in codeartifact_readers", project)
		}
		for _, name := range names {
			switch {
			case name == "":
				return fmt.Errorf("codeartifact_reader_environments[%s]: empty environment name", project)
			case strings.ContainsAny(name, " \t\n:*?"):
				return fmt.Errorf("codeartifact_reader_environments[%s]: environment name %q must be one literal name: no whitespace, ':' or wildcard", project, name)
			}
		}
	}

	return nil
}

// AllRepositoryNames returns all repository names in format "project/component".
func (c *Config) AllRepositoryNames() []string {
	var names []string
	for _, project := range c.Projects {
		for _, component := range project.Components {
			names = append(names, fmt.Sprintf("%s/%s", project.Name, component))
		}
	}
	return names
}

// GetProject returns a project by name, or nil if not found.
func (c *Config) GetProject(name string) *ProjectConfig {
	for i := range c.Projects {
		if c.Projects[i].Name == name {
			return &c.Projects[i]
		}
	}
	return nil
}

// ProjectNames returns all project names.
func (c *Config) ProjectNames() []string {
	names := make([]string, len(c.Projects))
	for i, project := range c.Projects {
		names[i] = project.Name
	}
	return names
}

// RepositoryNames returns ECR repository names for a specific project in format "project/component".
func (p *ProjectConfig) RepositoryNames() []string {
	names := make([]string, len(p.ECR))
	for i, component := range p.ECR {
		names[i] = fmt.Sprintf("%s/%s", p.Name, component)
	}
	return names
}

// ForceDelete reports whether the ECR repository of the project's component
// carries force-delete: true only for a component marked retiring, never
// otherwise.
func (c *Config) ForceDelete(project, component string) bool {
	return slices.Contains(c.Retiring[project], component)
}

// S3ComponentNames returns S3 component names for a specific project in format "project/component".
func (p *ProjectConfig) S3ComponentNames() []string {
	names := make([]string, len(p.S3))
	for i, component := range p.S3 {
		names[i] = fmt.Sprintf("%s/%s", p.Name, component)
	}
	return names
}

// CompanyProjects resolves a company-aggregated stack name to its project
// set: "infra" = projects with no owning company (platform tooling),
// otherwise the projects whose Company matches. Unknown names return nil,
// which sends the caller down the per-project path.
func CompanyProjects(c *Config, group string) []*ProjectConfig {
	var out []*ProjectConfig

	for i := range c.Projects {
		p := &c.Projects[i]
		if (group == "infra" && p.Company == "") || p.Company == group {
			out = append(out, p)
		}
	}

	// A group must aggregate more than one project to be a group name; a
	// single match could equally be a legacy per-project stack name.
	if group != "infra" && c.GetProject(group) != nil {
		return nil
	}

	return out
}
