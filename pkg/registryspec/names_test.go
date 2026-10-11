package registryspec_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/truvity/artifacts/pkg/registryspec"
)

func TestProjectComponentsListChartsBeforeImages(t *testing.T) {
	got := registryspec.ProjectComponents([]string{"app", "ops"}, []string{"api", "web"})
	want := []string{"charts/app", "charts/ops", "api", "web"}

	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	if got := registryspec.ProjectComponents(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty input must give an empty, non-nil list, got %#v", got)
	}

	if registryspec.ChartRepository("x") != "charts/x" {
		t.Fatal("chart repository naming")
	}
}

// TestNoNonStdlibImports guards this package's reason to exist: a caller
// must be able to import registryspec alone, with no Pulumi or AWS SDK
// riding along. `go list -deps` lists every package this one imports,
// transitively; an import path whose first segment contains a "." is a
// module outside the standard library.
func TestNoNonStdlibImports(t *testing.T) {
	const pkg = "github.com/truvity/artifacts/pkg/registryspec"

	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || line == pkg {
			continue
		}

		root, _, _ := strings.Cut(line, "/")
		if strings.Contains(root, ".") {
			t.Errorf("pkg/registryspec must depend on the standard library only, found %q", line)
		}
	}
}
