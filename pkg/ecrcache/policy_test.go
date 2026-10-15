package ecrcache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPullThroughPolicyJSON(t *testing.T) {
	t.Parallel()

	got, err := PullThroughPolicy("eu-central-1", "111122223333", "docker-hub")
	require.NoError(t, err)
	require.Equal(t, `{"Statement":[{"Action":["ecr:BatchImportUpstreamImage","ecr:CreateRepository",`+
		`"ecr:BatchGetImage","ecr:GetDownloadUrlForLayer","ecr:BatchCheckLayerAvailability"],`+
		`"Effect":"Allow","Resource":"arn:aws:ecr:eu-central-1:111122223333:repository/docker-hub/*",`+
		`"Sid":"PullThroughCache"}],"Version":"2012-10-17"}`, got)
}
