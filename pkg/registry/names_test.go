package registry_test

import (
	"slices"
	"testing"

	"github.com/truvity/artifacts/pkg/registry"
)

func TestProjectComponentsListChartsBeforeImages(t *testing.T) {
	got := registry.ProjectComponents([]string{"app", "ops"}, []string{"api", "web"})
	want := []string{"charts/app", "charts/ops", "api", "web"}

	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	if got := registry.ProjectComponents(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty input must give an empty, non-nil list, got %#v", got)
	}

	if registry.ChartRepository("x") != "charts/x" {
		t.Fatal("chart repository naming")
	}
}
