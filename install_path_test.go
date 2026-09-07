package forgepilot_test

import (
	"os"
	"strings"
	"testing"
)

// The documented `go install` target must be the module path itself. When the
// two drift the command fails against a repository that does not exist, and
// nothing in the build catches it: imports keep resolving locally.
func TestDocumentedInstallCommandUsesModulePath(t *testing.T) {
	module := modulePath(t)

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	want := "go install " + module + "/cmd/forgepilot@latest"
	if !strings.Contains(string(readme), want) {
		t.Fatalf("README.md does not document %q", want)
	}
}

// The module path must name the repository it is published from, otherwise
// `go install` resolves to a different (or absent) repository.
func TestModulePathMatchesRepositoryURL(t *testing.T) {
	module := modulePath(t)

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	clone := "https://" + module + ".git"
	if !strings.Contains(string(readme), clone) {
		t.Fatalf("README.md does not clone from %q; module path and repository URL disagree", clone)
	}
}

func modulePath(t *testing.T) string {
	t.Helper()

	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.SplitSeq(string(gomod), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(after)
		}
	}
	t.Fatal("go.mod has no module directive")
	return ""
}
