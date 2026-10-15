package registry

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// The JSON is pinned: these statements are attached to live roles, so a
// change to a single byte is a change to a deployed policy.
func TestCodeArtifactReadStatementsJSON(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(CodeArtifactReadStatements())
	require.NoError(t, err)
	require.JSONEq(t, `[
	 {"Sid":"CodeArtifactRead","Effect":"Allow","Action":["codeartifact:GetAuthorizationToken","codeartifact:GetRepositoryEndpoint","codeartifact:ReadFromRepository"],"Resource":"*"},
	 {"Sid":"CodeArtifactBearerToken","Effect":"Allow","Action":"sts:GetServiceBearerToken","Resource":"*","Condition":{"StringEquals":{"sts:AWSServiceName":"codeartifact.amazonaws.com"}}}
	]`, string(raw))
}

func TestECRPreviewStatementsJSON(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(ECRPreviewStatements("eu-central-1", "111122223333", []string{"proj", "other/x"}))
	require.NoError(t, err)
	require.Equal(t, `[{"Action":"ecr:GetAuthorizationToken","Effect":"Allow","Resource":"*","Sid":"EcrAuth"},`+
		`{"Action":["ecr:BatchGetImage","ecr:GetDownloadUrlForLayer","ecr:BatchCheckLayerAvailability","ecr:BatchImportUpstreamImage","ecr:CreateRepository"],"Effect":"Allow",`+
		`"Resource":["arn:aws:ecr:eu-central-1:111122223333:repository/docker-hub/*","arn:aws:ecr:eu-central-1:111122223333:repository/ghcr/*","arn:aws:ecr:eu-central-1:111122223333:repository/quay/*","arn:aws:ecr:eu-central-1:111122223333:repository/github/*"],"Sid":"EcrPullThroughRead"},`+
		`{"Action":["ecr:PutImage","ecr:InitiateLayerUpload","ecr:UploadLayerPart","ecr:CompleteLayerUpload","ecr:BatchCheckLayerAvailability","ecr:BatchGetImage","ecr:GetDownloadUrlForLayer","ecr:DescribeRepositories","ecr:ListImages","ecr:DescribeImages"],"Effect":"Allow",`+
		`"Resource":["arn:aws:ecr:eu-central-1:111122223333:repository/proj/*","arn:aws:ecr:eu-central-1:111122223333:repository/other/x/*","arn:aws:ecr:eu-central-1:111122223333:repository/ci/buildkit-cache"],"Sid":"EcrPreviewPush"}]`, string(raw))
}

func TestECRPreviewStatementsWithoutPushOmitsThePushStatement(t *testing.T) {
	t.Parallel()

	require.Len(t, ECRPreviewStatements("r", "a", nil), 2)
}
