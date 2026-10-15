package registry

import "fmt"

// IAM statement builders for the principals that consume a registry rather
// than own it: the CodeArtifact read surface and the preview-ECR surface of
// a CI pool and its engineers. They return plain statement maps so a caller
// composes them into whichever policy document it attaches; the maps are
// the exact JSON shape of an IAM statement.

const (
	stmtSid       = "Sid"
	stmtEffect    = "Effect"
	stmtAllow     = "Allow"
	stmtAction    = "Action"
	stmtResource  = "Resource"
	stmtCondition = "Condition"

	ecrGetAuthorizationToken = "ecr:GetAuthorizationToken"
)

// pullThroughReadPrefixes are the public-registry caches every build pulls
// base images through.
var pullThroughReadPrefixes = []string{"docker-hub", "ghcr", "quay", "github"}

// CodeArtifactReadStatements is the npm-registry read surface: the token and
// endpoint calls, the repository read, and the STS bearer-token permission
// without which CodeArtifact tokens cannot be minted (its absence fails as
// AccessDenied on GetAuthorizationToken, which reads like the read grant is
// missing). Resources are "*": the domain and repository ARNs are not known
// to the caller, and read on the account's own CodeArtifact is the whole
// intended surface.
func CodeArtifactReadStatements() []map[string]any {
	return []map[string]any{
		{
			stmtSid:    "CodeArtifactRead",
			stmtEffect: stmtAllow,
			stmtAction: []string{
				"codeartifact:GetAuthorizationToken",
				"codeartifact:GetRepositoryEndpoint",
				"codeartifact:ReadFromRepository",
			},
			stmtResource: "*",
		},
		{
			stmtSid:      "CodeArtifactBearerToken",
			stmtEffect:   stmtAllow,
			stmtAction:   "sts:GetServiceBearerToken",
			stmtResource: "*",
			stmtCondition: map[string]any{
				"StringEquals": map[string]any{
					"sts:AWSServiceName": "codeartifact.amazonaws.com",
				},
			},
		},
	}
}

// ECRPreviewStatements is the preview-registry surface: authenticate, read
// through the pull-through caches, and push snapshots into the repositories
// under each of previewPush's prefixes plus the shared BuildKit cache
// repository.
//
// Registry auth and pull sit beside the push because they are the same job:
// a role that can push but not authenticate is a half-state nobody wants to
// debug. BatchImportUpstreamImage and CreateRepository are what populate a
// cache on a miss, so every puller needs them. With no previewPush the
// push statement is omitted.
func ECRPreviewStatements(region, accountID string, previewPush []string) []map[string]any {
	repo := func(path string) string {
		return fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", region, accountID, path)
	}

	readARNs := make([]string, 0, len(pullThroughReadPrefixes))
	for _, p := range pullThroughReadPrefixes {
		readARNs = append(readARNs, repo(p+"/*"))
	}

	statements := []map[string]any{
		{
			// Not resource-scopable. Harmless alone: a login without
			// pull or push rights reads and writes nothing.
			stmtSid:      "EcrAuth",
			stmtEffect:   stmtAllow,
			stmtAction:   ecrGetAuthorizationToken,
			stmtResource: "*",
		},
		{
			stmtSid:    "EcrPullThroughRead",
			stmtEffect: stmtAllow,
			stmtAction: []string{
				ecrBatchGetImage, ecrGetDownloadURL, ecrBatchCheckLayer,
				"ecr:BatchImportUpstreamImage",
				"ecr:CreateRepository",
			},
			stmtResource: readARNs,
		},
	}

	if len(previewPush) == 0 {
		return statements
	}

	arns := make([]string, 0, len(previewPush)+1)
	for _, p := range previewPush {
		arns = append(arns, repo(p+"/*"))
	}

	arns = append(arns, repo("ci/buildkit-cache"))

	return append(statements, map[string]any{
		stmtSid:    "EcrPreviewPush",
		stmtEffect: stmtAllow,
		stmtAction: []string{
			ecrPutImage,
			ecrInitiateLayerUpload,
			ecrUploadLayerPart,
			ecrCompleteLayerUpload,
			ecrBatchCheckLayer,
			ecrBatchGetImage,
			ecrGetDownloadURL,
			ecrDescribeRepositories,
			ecrListImages,
			ecrDescribeImages,
		},
		stmtResource: arns,
	})
}
