package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// policyDoc is the shape we assert against — buildPolicyJSON's output parsed
// back, so the tests check what AWS would actually receive.
type (
	policyDoc struct {
		Statement []struct {
			Sid       string `json:"Sid"`
			Effect    string `json:"Effect"`
			Principal struct {
				AWS     any `json:"AWS"`
				Service any `json:"Service"`
			} `json:"Principal"`
			Action any `json:"Action"`
		} `json:"Statement"`
	}
)

func parsePolicy(t *testing.T, statements []iam.GetPolicyDocumentStatement) policyDoc {
	t.Helper()

	raw, err := buildPolicyJSON(statements)
	if err != nil {
		t.Fatalf("buildPolicyJSON: %v", err)
	}

	var doc policyDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("parse policy JSON: %v\n%s", err, raw)
	}

	return doc
}

// A writer-role statement carries push actions for exactly the named roles —
// the fine-grained publish identity the stable tier depends on.
func TestWriterRoleStatement(t *testing.T) {
	effectAllow := string(iam.PolicyStatementEffectALLOW)
	roles := []string{
		"arn:aws:iam::111122223333:role/release-billing",
		"arn:aws:iam::111122223333:role/release-svc",
	}

	doc := parsePolicy(t, []iam.GetPolicyDocumentStatement{{
		Sid:    pulumi.StringRef("AllowNamedWriterRolesPush"),
		Effect: &effectAllow,
		Principals: []iam.GetPolicyDocumentStatementPrincipal{{
			Type:        principalAWS,
			Identifiers: roles,
		}},
		Actions: []string{ecrGetDownloadURL, ecrPutImage},
	}})

	if len(doc.Statement) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(doc.Statement))
	}

	st := doc.Statement[0]
	if st.Sid != "AllowNamedWriterRolesPush" || st.Effect != "Allow" {
		t.Errorf("unexpected sid/effect: %+v", st)
	}

	principals, ok := st.Principal.AWS.([]any)
	if !ok {
		t.Fatalf("expected a list of AWS principals, got %T", st.Principal.AWS)
	}

	if len(principals) != len(roles) {
		t.Errorf("expected %d principals, got %d", len(roles), len(principals))
	}

	// Roles, never account roots: an account root here would silently restore
	// account-wide push and defeat the whole point.
	for _, p := range principals {
		s, _ := p.(string)
		if strings.HasSuffix(s, ":root") {
			t.Errorf("writer principal must be a role, got account root: %q", s)
		}
	}

	// Push actions present, admin actions absent (writers publish, they do
	// not manage repository policy).
	actions := actionSet(t, st.Action)
	for _, want := range []string{ecrPutImage} {
		if !actions[want] {
			t.Errorf("missing push action %q", want)
		}
	}

	for _, forbidden := range []string{"ecr:SetRepositoryPolicy", "ecr:DeleteRepository", "ecr:GetRepositoryPolicy"} {
		if actions[forbidden] {
			t.Errorf("writer roles must not hold admin action %q", forbidden)
		}
	}
}

// An empty WriterRoleARNs list must produce no writer statement at all —
// that is what keeps today's behavior byte-identical while the mechanism
// lands ahead of the release workflows that will use it.
func TestWriterRoleStatement_EmptyIsNoop(t *testing.T) {
	doc := parsePolicy(t, []iam.GetPolicyDocumentStatement{})

	for _, st := range doc.Statement {
		if st.Sid == "AllowNamedWriterRolesPush" {
			t.Error("no writer roles configured, but a writer statement was emitted")
		}
	}
}

func actionSet(t *testing.T, raw any) map[string]bool {
	t.Helper()

	out := map[string]bool{}

	switch v := raw.(type) {
	case string:
		out[v] = true
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok {
				out[s] = true
			}
		}
	default:
		t.Fatalf("unexpected Action shape %T", raw)
	}

	return out
}
