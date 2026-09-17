package forgepilot_test

import (
	"os"
	"strings"
	"testing"
)

// Formal onboarding must not resolve a moving source revision. A reviewed
// full commit SHA is supplied by the onboarding procedure once its native-Mac
// acceptance is complete; until then the public README must not invent any
// go-install shortcut with a branch, tag, or floating version.
func TestReadmeDoesNotAdvertiseGoInstallBeforeOnboardingAcceptance(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	if strings.Contains(string(readme), "go install "+modulePath(t)+"/cmd/forgepilot@") {
		t.Fatal("README.md advertises an unpublished go-install onboarding command")
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
