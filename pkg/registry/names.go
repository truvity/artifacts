package registry

import "github.com/truvity/artifacts/pkg/registryspec"

// The repository naming rules live in pkg/registryspec, which links no
// Pulumi or AWS SDK; they are re-exported here so existing callers keep
// compiling. A caller that only names repositories should import
// pkg/registryspec directly.

// ChartRepositoryPrefix is the path under a project in which the repositories
// of its Helm charts live. See registryspec.ChartRepositoryPrefix.
const ChartRepositoryPrefix = registryspec.ChartRepositoryPrefix

// ChartRepository is the component name, under a project, of the repository of
// one of its Helm charts.
func ChartRepository(chart string) string { return registryspec.ChartRepository(chart) }

// ProjectComponents is the list of repository component names of a project:
// its chart repositories first, in order, then its images, in order.
func ProjectComponents(charts, images []string) []string {
	return registryspec.ProjectComponents(charts, images)
}
