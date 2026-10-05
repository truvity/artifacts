package registry

// ECR/S3 IAM policy constants.
const (
	principalAWS = "AWS"

	ecrGetDownloadURL  = "ecr:GetDownloadUrlForLayer"
	ecrBatchGetImage   = "ecr:BatchGetImage"
	ecrBatchCheckLayer = "ecr:BatchCheckLayerAvailability"

	// Push + describe actions shared by the owner-account and named-writer
	// statements (see AccessConfig.WriterRoleARNs).
	ecrPutImage             = "ecr:PutImage"
	ecrDescribeRepositories = "ecr:DescribeRepositories"
	ecrListImages           = "ecr:ListImages"
	ecrDescribeImages       = "ecr:DescribeImages"
	ecrInitiateLayerUpload  = "ecr:InitiateLayerUpload"
	ecrUploadLayerPart      = "ecr:UploadLayerPart"
	ecrCompleteLayerUpload  = "ecr:CompleteLayerUpload"
	s3GetObject             = "s3:GetObject"

	lifecycleCountType = "imageCountMoreThan"
	lifecycleAction    = "expire"
)
