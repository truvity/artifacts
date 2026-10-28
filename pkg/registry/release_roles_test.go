package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

type (
	trustDoc struct {
		Statement []struct {
			Sid       string `json:"Sid"`
			Effect    string `json:"Effect"`
			Principal struct {
				Federated any `json:"Federated"`
				AWS       any `json:"AWS"`
			} `json:"Principal"`
			Action    any `json:"Action"`
			Condition struct {
				StringEquals map[string]any `json:"StringEquals"`
				StringLike   map[string]any `json:"StringLike"`
			} `json:"Condition"`
		} `json:"Statement"`
	}

	permDoc struct {
		Statement []struct {
			Sid      string `json:"Sid"`
			Action   any    `json:"Action"`
			Resource any    `json:"Resource"`
		} `json:"Statement"`
	}
)

const (
	testProviderARN = "arn:aws:iam::111122223333:oidc-provider/token.actions.githubusercontent.com"
)

func mustTrust(t *testing.T, cfg ReleaseRoleConfig) trustDoc {
	t.Helper()

	raw, err := buildReleaseTrustPolicy(testProviderARN, cfg)
	if err != nil {
		t.Fatalf("buildReleaseTrustPolicy: %v", err)
	}

	var doc trustDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("parse trust policy: %v\n%s", err, raw)
	}

	return doc
}

// The CI trust statement must pin BOTH audience and subject. An unpinned
// federated trust would let any repository on GitHub — anyone's — assume the
// role, which is the classic GitHub-OIDC misconfiguration.
func TestReleaseTrustPolicy_GitHubStatementIsPinned(t *testing.T) {
	doc := mustTrust(t, ReleaseRoleConfig{
		Project:         "billing",
		SubjectPatterns: []string{"repo:acme/billing:ref:refs/tags/*"},
		RepositoryARNs:  []string{"arn:aws:ecr:eu-central-1:111122223333:repository/billing/api"},
	})

	if len(doc.Statement) != 1 {
		t.Fatalf("expected only the CI statement, got %d", len(doc.Statement))
	}

	st := doc.Statement[0]
	if st.Sid != "GitHubActionsOIDC" || st.Effect != "Allow" {
		t.Errorf("unexpected statement: %+v", st)
	}

	if st.Action != "sts:AssumeRoleWithWebIdentity" {
		t.Errorf("expected web-identity assume, got %v", st.Action)
	}

	if got := st.Principal.Federated; got != testProviderARN {
		t.Errorf("federated principal = %v, want the account's OIDC provider", got)
	}

	if aud := st.Condition.StringEquals["token.actions.githubusercontent.com:aud"]; aud != githubOIDCAudience {
		t.Errorf("audience condition missing or wrong: %v", aud)
	}

	subs, ok := st.Condition.StringLike["token.actions.githubusercontent.com:sub"].([]any)
	if !ok || len(subs) == 0 {
		t.Fatalf("subject condition missing: %+v", st.Condition.StringLike)
	}

	for _, s := range subs {
		str, _ := s.(string)
		if !strings.HasPrefix(str, "repo:") {
			t.Errorf("subject pattern %q must scope to a repo", str)
		}

		if str == "*" || strings.HasPrefix(str, "*") {
			t.Errorf("subject pattern %q is unbounded — any GitHub repo could assume this role", str)
		}
	}
}

// Publishing must remain possible by hand once repository policies stop
// granting account-wide push: one identity per project, reached two ways.
func TestReleaseTrustPolicy_HumanStatement(t *testing.T) {
	doc := mustTrust(t, ReleaseRoleConfig{
		Project:              "billing",
		SubjectPatterns:      []string{"repo:acme/billing:environment:release"},
		OwnerAccountID:       "111122223333",
		HumanRoleARNPatterns: []string{"arn:aws:iam::111122223333:role/aws-reserved/sso.amazonaws.com/*/AWSReservedSSO_admin_*"},
		RepositoryARNs:       []string{"arn:aws:ecr:eu-central-1:111122223333:repository/billing/api"},
	})

	if len(doc.Statement) != 2 {
		t.Fatalf("expected CI + human statements, got %d", len(doc.Statement))
	}

	human := doc.Statement[1]
	if human.Sid != "HumanBreakGlassPublish" || human.Action != "sts:AssumeRole" {
		t.Errorf("unexpected human statement: %+v", human)
	}

	// Principal must be the account root; the ACTUAL caller is pinned by the
	// aws:PrincipalArn condition, because trust-policy principals don't
	// support wildcards and AWSReservedSSO names carry random suffixes.
	if root, _ := human.Principal.AWS.(string); root != "arn:aws:iam::111122223333:root" {
		t.Errorf("human principal must be the owner account root, got %v", human.Principal.AWS)
	}

	pats, ok := human.Condition.StringLike["aws:PrincipalArn"].([]any)
	if !ok || len(pats) == 0 {
		t.Fatalf("human statement must pin callers via aws:PrincipalArn, got %+v", human.Condition)
	}
}

// Human trust anchored on a root principal without an account ID would be
// malformed; refuse to build it.
func TestReleaseTrustPolicy_HumanRequiresAccountID(t *testing.T) {
	_, err := buildReleaseTrustPolicy(testProviderARN, ReleaseRoleConfig{
		Project:              "billing",
		HumanRoleARNPatterns: []string{"arn:aws:iam::111122223333:role/aws-reserved/sso.amazonaws.com/*/AWSReservedSSO_admin_*"},
	})
	if err == nil {
		t.Fatal("expected an error when OwnerAccountID is missing")
	}
}

// A role with neither CI subjects nor human principals would be assumable by
// nobody (or worse, if a future edit dropped the conditions, by anybody) —
// refuse to build it.
func TestReleaseTrustPolicy_RejectsEmptyTrust(t *testing.T) {
	_, err := buildReleaseTrustPolicy(testProviderARN, ReleaseRoleConfig{Project: "billing"})
	if err == nil {
		t.Fatal("expected an error for a role with no trust statements")
	}
}

// GetAuthorizationToken has no resource — it is the reason cross-account ECR
// push cannot be granted by a repository policy alone. Push must be scoped to
// the project's repositories, and admin actions must never appear.
func TestReleasePermissionPolicy(t *testing.T) {
	repos := []string{"arn:aws:ecr:eu-central-1:111122223333:repository/billing/api"}

	raw, err := buildReleasePermissionPolicy(repos)
	if err != nil {
		t.Fatalf("buildReleasePermissionPolicy: %v", err)
	}

	var doc permDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("parse permission policy: %v\n%s", err, raw)
	}

	if len(doc.Statement) != 2 {
		t.Fatalf("expected login + push statements, got %d", len(doc.Statement))
	}

	login := doc.Statement[0]
	if login.Action != "ecr:GetAuthorizationToken" || login.Resource != "*" {
		t.Errorf("login statement must be GetAuthorizationToken on *, got %+v", login)
	}

	push := doc.Statement[1]
	if res, ok := push.Resource.([]any); !ok || len(res) != len(repos) {
		t.Errorf("push must be scoped to the project's repositories, got %v", push.Resource)
	}

	actions := map[string]bool{}
	if list, ok := push.Action.([]any); ok {
		for _, a := range list {
			if s, ok := a.(string); ok {
				actions[s] = true
			}
		}
	}

	if !actions[ecrPutImage] {
		t.Error("push statement missing ecr:PutImage")
	}

	for _, forbidden := range []string{"ecr:SetRepositoryPolicy", "ecr:DeleteRepository", "ecr:CreateRepository"} {
		if actions[forbidden] {
			t.Errorf("publish identity must not hold admin action %q", forbidden)
		}
	}
}

// With no workflow ref patterns the CI statement is the one built from
// `sub` alone, byte for byte: the field is opt-in and must not change a
// deployed policy.
func TestReleaseTrustPolicy_NoWorkflowRefIsToday(t *testing.T) {
	base := ReleaseRoleConfig{
		Project:         "billing",
		SubjectPatterns: []string{"repo:acme/billing:ref:refs/tags/*"},
	}

	raw, err := buildReleaseTrustPolicy(testProviderARN, base)
	if err != nil {
		t.Fatalf("buildReleaseTrustPolicy: %v", err)
	}

	const want = `{"Statement":[{"Action":"sts:AssumeRoleWithWebIdentity",` +
		`"Condition":{"StringEquals":{"token.actions.githubusercontent.com:aud":"sts.amazonaws.com"},` +
		`"StringLike":{"token.actions.githubusercontent.com:sub":["repo:acme/billing:ref:refs/tags/*"]}},` +
		`"Effect":"Allow","Principal":{"Federated":"` + testProviderARN + `"},"Sid":"GitHubActionsOIDC"}],` +
		`"Version":"2012-10-17"}`
	if raw != want {
		t.Errorf("policy changed with the field empty:\n got %s\nwant %s", raw, want)
	}
}

func TestReleaseTrustPolicy_WorkflowRefCondition(t *testing.T) {
	raw, err := buildReleaseTrustPolicy(testProviderARN, ReleaseRoleConfig{
		Project:         "billing",
		SubjectPatterns: []string{"repo:acme/billing:ref:refs/tags/*"},
		WorkflowRefPatterns: []string{
			"acme/billing/.github/workflows/release.yaml@refs/tags/v*",
			"acme/ci-workflows/.github/workflows/release-private.yaml@refs/tags/v5.*",
		},
	})
	if err != nil {
		t.Fatalf("buildReleaseTrustPolicy: %v", err)
	}

	const want = `{"StringEquals":{"token.actions.githubusercontent.com:aud":"sts.amazonaws.com"},` +
		`"StringLike":{"token.actions.githubusercontent.com:job_workflow_ref":` +
		`["acme/billing/.github/workflows/release.yaml@refs/tags/v*","acme/ci-workflows/.github/workflows/release-private.yaml@refs/tags/v5.*"],` +
		`"token.actions.githubusercontent.com:sub":["repo:acme/billing:ref:refs/tags/*"]}}`

	var doc struct {
		Statement []struct {
			Condition json.RawMessage `json:"Condition"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(doc.Statement) != 1 {
		t.Fatalf("expected the CI statement only, got %d", len(doc.Statement))
	}

	if got := string(doc.Statement[0].Condition); got != want {
		t.Errorf("condition:\n got %s\nwant %s", got, want)
	}
}

// The human statement is never conditioned on the workflow: a person has
// no job_workflow_ref.
func TestReleaseTrustPolicy_WorkflowRefLeavesHumanStatementAlone(t *testing.T) {
	doc := mustTrust(t, ReleaseRoleConfig{
		Project:              "billing",
		SubjectPatterns:      []string{"repo:acme/billing:ref:refs/tags/*"},
		WorkflowRefPatterns:  []string{"acme/billing/.github/workflows/release.yaml@refs/tags/v*"},
		OwnerAccountID:       "111122223333",
		HumanRoleARNPatterns: []string{"arn:aws:iam::111122223333:role/x"},
	})

	if len(doc.Statement) != 2 {
		t.Fatalf("want 2 statements, got %d", len(doc.Statement))
	}

	if _, ok := doc.Statement[1].Condition.StringLike["token.actions.githubusercontent.com:job_workflow_ref"]; ok {
		t.Error("human statement must not carry job_workflow_ref")
	}
}

func TestReleaseTrustPolicy_WorkflowRefValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		subjects []string
		refs     []string
	}{
		"empty pattern":          {[]string{"repo:a/b:ref:refs/tags/*"}, []string{""}},
		"empty among valid":      {[]string{"repo:a/b:ref:refs/tags/*"}, []string{"a/b/.github/workflows/r.yaml@refs/tags/v*", ""}},
		"space":                  {[]string{"repo:a/b:ref:refs/tags/*"}, []string{"a/b/.github/workflows/r.yaml @refs/tags/v*"}},
		"tab":                    {[]string{"repo:a/b:ref:refs/tags/*"}, []string{"a/b/.github/workflows/r.yaml\t@refs/tags/v*"}},
		"newline":                {[]string{"repo:a/b:ref:refs/tags/*"}, []string{"a/b/.github/workflows/r.yaml@refs/tags/v*\n"}},
		"no subject patterns":    {nil, []string{"a/b/.github/workflows/r.yaml@refs/tags/v*"}},
		"empty subject patterns": {[]string{}, []string{"a/b/.github/workflows/r.yaml@refs/tags/v*"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildReleaseTrustPolicy(testProviderARN, ReleaseRoleConfig{
				Project:              "billing",
				SubjectPatterns:      tc.subjects,
				WorkflowRefPatterns:  tc.refs,
				OwnerAccountID:       "111122223333",
				HumanRoleARNPatterns: []string{"arn:aws:iam::111122223333:role/x"},
			})
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
