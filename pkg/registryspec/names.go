// Package registryspec holds the pure naming rules of the registry: which
// repositories a project owns. No AWS call, no Pulumi resource, no SDK
// import of any kind: an estate's config layer that only names repositories
// links this package and not the Pulumi and AWS SDKs that pkg/registry
// imports to create them. pkg/registry re-exports everything here, so
// existing callers keep compiling.
//
// TestNoNonStdlibImports fails the moment this package imports anything
// outside the standard library.
package registryspec

// ChartRepositoryPrefix is the path under a project in which the repositories
// of its Helm charts live: a chart `app` of project `p` is the repository
// `p/charts/app`, and images are `p/<image>`.
const ChartRepositoryPrefix = "charts/"

// ChartRepository is the component name, under a project, of the repository of
// one of its Helm charts.
func ChartRepository(chart string) string {
	return ChartRepositoryPrefix + chart
}

// ProjectComponents is the list of repository component names of a project
// (what a project's Components or ECR list takes): its chart repositories first,
// in order, then its images, in order.
func ProjectComponents(charts, images []string) []string {
	out := make([]string, 0, len(charts)+len(images))

	for _, chart := range charts {
		out = append(out, ChartRepository(chart))
	}

	return append(out, images...)
}
