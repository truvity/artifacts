package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The subject is the trust boundary; these cases are what it is allowed
// to say. A project whose repository GitHub signs with immutable
// identifiers pins the whole head — composing "repo:{owner}/{repo}" for
// it produces a subject no token ever carries, and the release dies at
// AssumeRoleWithWebIdentity (a release).
func TestReleaseSubject(t *testing.T) {
	t.Parallel()

	cfg := &ReleaseConfig{
		SourceRepo:    "acme/mono",
		SubjectPrefix: "repo:acme/mono",
		Overrides: map[string]ReleaseOverride{
			// Its own repository, so no {project}/ prefix on the tag —
			// and signed with immutable identifiers, so the subject's
			// head is pinned rather than composed from the names.
			"app": {
				SourceRepo:    "acme/app",
				TagPattern:    "v*",
				SubjectPrefix: "repo:acme@10/app@20",
			},
			// Repo moved, tag shape kept: the two are independent.
			"partial": {
				SourceRepo:    "acme/partial",
				SubjectPrefix: "repo:acme/partial",
			},
		},
	}

	for name, tc := range map[string]struct {
		project string
		want    string
	}{
		"monorepo project keeps repo and prefix": {
			"svc", "repo:acme/mono:ref:refs/tags/svc/v*",
		},
		"extracted project: pinned subject, plain tag": {
			"app", "repo:acme@10/app@20:ref:refs/tags/v*",
		},
		"override may move the repo alone": {
			"partial", "repo:acme/partial:ref:refs/tags/partial/v*",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := releaseSubject(cfg, tc.project)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Subject)
		})
	}
}

// The override must not leak: a project without one keeps the estate
// default even when other projects override. A bug here would hand one
// project's tags authority over another's artifacts.
func TestReleaseSubjectOverrideDoesNotLeak(t *testing.T) {
	t.Parallel()

	cfg := &ReleaseConfig{
		SourceRepo:    "acme/mono",
		SubjectPrefix: "repo:acme/mono",
		Overrides: map[string]ReleaseOverride{"app": {
			SourceRepo:    "acme/app",
			TagPattern:    "v*",
			SubjectPrefix: "repo:acme@10/app@20",
		}},
	}

	for _, p := range []string{"svc", "short", "billing"} {
		got, err := releaseSubject(cfg, p)
		require.NoError(t, err)
		require.Contains(t, got.Subject, "repo:acme/mono:", "%s must still publish from the monorepo", p)
		require.Contains(t, got.Subject, "refs/tags/"+p+"/v*", "%s must keep its prefixed tag", p)
	}
}

// An unstated prefix must STOP the deploy, and say what to run. The
// alternative is what this whole change removes: composing
// "repo:{owner}/{repo}", which GitHub signs for some repositories and
// not others, and finding out at tag time.
func TestReleaseSubjectRefusesUnstatedPrefix(t *testing.T) {
	t.Parallel()

	t.Run("estate prefix missing", func(t *testing.T) {
		t.Parallel()

		_, err := releaseSubject(&ReleaseConfig{SourceRepo: "acme/mono"}, "svc")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "release.subject_prefix is unset")
		assert.Contains(t, err.Error(), "gh api /repos/acme/mono/actions/oidc/customization/sub")
	})

	// The sharper case: the estate prefix IS set, and inheriting it would
	// deploy a role trusting mono's tokens under another project's name —
	// a subject that authenticates, for the wrong repository.
	t.Run("extracted project inherits nothing", func(t *testing.T) {
		t.Parallel()

		_, err := releaseSubject(&ReleaseConfig{
			SourceRepo:    "acme/mono",
			SubjectPrefix: "repo:acme/mono",
			Overrides:     map[string]ReleaseOverride{"app": {SourceRepo: "acme/app", TagPattern: "v*"}},
		}, "app")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source_repo acme/app without subject_prefix")
		assert.Contains(t, err.Error(), "gh api /repos/acme/app/actions/oidc/customization/sub")
	})
}

// The drift check compares the SAME rows the deploy writes — that is the
// point of walking ReleaseTargets in both. These cases pin what counts as
// drift: a differing prefix, and a repository GitHub answered nothing for
// (a subject nobody can confirm is a subject nobody should deploy).
func TestCompareSubjects(t *testing.T) {
	t.Parallel()

	targets := []ReleaseTarget{
		{Project: "svc", Repo: "acme/mono", Prefix: "repo:acme/mono"},
		{Project: "app", Repo: "acme/app", Prefix: "repo:acme@1/app@2"},
		{Project: "ghost", Repo: "acme/ghost", Prefix: "repo:acme/ghost"},
	}

	drifts := CompareSubjects(targets, map[string]string{
		"acme/mono": "repo:acme/mono",
		// The rollout shape, against a config that still states names.
		"acme/app": "repo:acme@10/app@20",
	})

	require.Len(t, drifts, 2)
	assert.Equal(t, "app", drifts[0].Project)
	assert.Equal(t, "repo:acme@10/app@20", drifts[0].Want)
	assert.Equal(t, "repo:acme@1/app@2", drifts[0].Got)
	assert.Equal(t, "ghost", drifts[1].Project, "an unanswered repository is drift, not a pass")
}

// The npm-read role and the publish role must name the SAME identity: the
// point of deriving both from releaseSubject is that a release either has
// both halves or neither. A PR build, a branch build or another
// repository must be able to obtain neither.
func TestCodeArtifactReaderSharesTheReleaseSubject(t *testing.T) {
	t.Parallel()

	cfg := &ReleaseConfig{
		SourceRepo:    "acme/mono",
		SubjectPrefix: "repo:acme@1/mono@2",
		Overrides: map[string]ReleaseOverride{"app": {
			SourceRepo:    "acme/app",
			TagPattern:    "v*",
			SubjectPrefix: "repo:acme@1/app@3",
		}},
		CodeArtifactReaders: []string{"app"},
	}

	target, err := releaseSubject(cfg, cfg.CodeArtifactReaders[0])
	require.NoError(t, err)
	assert.Equal(t, "repo:acme@1/app@3:ref:refs/tags/v*", target.Subject)
	assert.NotContains(t, target.Subject, "refs/heads/", "no branch may read the private registry")
	assert.NotContains(t, target.Subject, "mono", "the reader must not inherit the monorepo's identity")
}

// A repository that cuts more than one release-tag shape (app: backend
// `v*` plus SDK `sdk-typescript/v*` / `sdk-java/v*`) trusts the
// extra tag refs on the READ-only reader role. The extras name the SAME
// repository as the base subject — same prefix, different tag ref — so a
// branch, a PR, or another repository still matches none. Without this,
// an SDK-tag publish cannot read the private registry and dies at
// AssumeRoleWithWebIdentity.
func TestCodeArtifactReaderTrustsExtraTagRefs(t *testing.T) {
	t.Parallel()

	target := ReleaseTarget{
		Project: "app",
		Repo:    "acme/app",
		Prefix:  "repo:acme@1/app@3",
		Subject: "repo:acme@1/app@3:ref:refs/tags/v*",
	}

	subjects := codeArtifactReaderSubjects(target, []string{"sdk-typescript/v*", "sdk-java/v*"}, nil)

	assert.Equal(t, []string{
		"repo:acme@1/app@3:ref:refs/tags/v*",
		"repo:acme@1/app@3:ref:refs/tags/sdk-typescript/v*",
		"repo:acme@1/app@3:ref:refs/tags/sdk-java/v*",
	}, subjects, "reader trusts the base tag plus each extra SDK tag, same repository")

	for _, s := range subjects {
		assert.Contains(t, s, "repo:acme@1/app@3:", "every extra subject names the same repository")
		assert.NotContains(t, s, "refs/heads/", "no branch may read the private registry")
	}
}

// No extra tags is the common case: the reader trusts exactly the base
// publish subject and nothing more.
func TestCodeArtifactReaderSubjectsWithoutExtras(t *testing.T) {
	t.Parallel()

	target := ReleaseTarget{
		Prefix:  "repo:acme@1/app@3",
		Subject: "repo:acme@1/app@3:ref:refs/tags/v*",
	}

	assert.Equal(t,
		[]string{"repo:acme@1/app@3:ref:refs/tags/v*"},
		codeArtifactReaderSubjects(target, nil, nil),
	)
}

// A job that references a GitHub environment is signed with
// `...:environment:<name>` in place of the tag-ref subject, so the reader
// trusts that shape too when the project declares it. app's TypeScript SDK
// publishes to npmjs by trusted publishing under `npm-publish`, whose
// deployment rule admits only `sdk-typescript/v*` tags: the ref gate moves
// to the environment, the subject still names the SAME repository, and the
// tag subjects stay as they are for the runs that carry no environment.
func TestCodeArtifactReaderTrustsEnvironments(t *testing.T) {
	t.Parallel()

	target := ReleaseTarget{
		Project: "app",
		Repo:    "acme/app",
		Prefix:  "repo:acme@1/app@3",
		Subject: "repo:acme@1/app@3:ref:refs/tags/v*",
	}

	subjects := codeArtifactReaderSubjects(target, []string{"sdk-java/v*"}, []string{"npm-publish"})

	assert.Equal(t, []string{
		"repo:acme@1/app@3:ref:refs/tags/v*",
		"repo:acme@1/app@3:ref:refs/tags/sdk-java/v*",
		"repo:acme@1/app@3:environment:npm-publish",
	}, subjects, "the environment subject joins the tag subjects; nothing is replaced")

	for _, s := range subjects {
		assert.Contains(t, s, "repo:acme@1/app@3:", "every subject names the same repository")
		assert.NotContains(t, s, "refs/heads/", "no branch may read the private registry")
	}
}

// An environment name is matched verbatim by GitHub, so config load takes
// exactly one literal name per entry, for a declared reader only.
func TestValidateCodeArtifactReaderEnvironments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		envs    map[string][]string
		wantErr string
	}{
		{name: "npm publish", envs: map[string][]string{"app": {"npm-publish"}}},
		{name: "none", envs: nil},
		{name: "not a reader", envs: map[string][]string{"mono": {"npm-publish"}}, wantErr: "not in codeartifact_readers"},
		{name: "wildcard", envs: map[string][]string{"app": {"*"}}, wantErr: "literal"},
		{name: "glob", envs: map[string][]string{"app": {"npm-*"}}, wantErr: "literal"},
		{name: "empty", envs: map[string][]string{"app": {""}}, wantErr: "empty"},
		{name: "colon", envs: map[string][]string{"app": {"npm-publish:ref:refs/heads/main"}}, wantErr: "literal"},
		{name: "whitespace", envs: map[string][]string{"app": {"npm publish"}}, wantErr: "literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateCodeArtifactReaderEnvironments(&ReleaseConfig{
				CodeArtifactReaders:            []string{"app"},
				CodeArtifactReaderEnvironments: tc.envs,
			})
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// The extras widen a trust policy, so config load refuses anything that
// would widen it further than one named tag family of a declared reader.
func TestValidateCodeArtifactReaderExtraTags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		extras  map[string][]string
		wantErr string
	}{
		{name: "sdk families", extras: map[string][]string{"app": {"sdk-typescript/v*", "sdk-java/v*"}}},
		{name: "none", extras: nil},
		{name: "not a reader", extras: map[string][]string{"mono": {"sdk-typescript/v*"}}, wantErr: "not in codeartifact_readers"},
		{name: "bare wildcard", extras: map[string][]string{"app": {"*"}}, wantErr: "literal"},
		{name: "wildcard before family", extras: map[string][]string{"app": {"sdk-*/v*"}}, wantErr: "literal"},
		{name: "no family", extras: map[string][]string{"app": {"v*"}}, wantErr: "literal"},
		{name: "full ref", extras: map[string][]string{"app": {"refs/tags/sdk-java/v*"}}, wantErr: "after refs/tags/"},
		{name: "empty", extras: map[string][]string{"app": {""}}, wantErr: "empty"},
		{name: "colon", extras: map[string][]string{"app": {"sdk-java/v*:environment:prod"}}, wantErr: "':'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateCodeArtifactReaderExtraTags(&ReleaseConfig{
				CodeArtifactReaders:         []string{"app"},
				CodeArtifactReaderExtraTags: tc.extras,
			})
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
