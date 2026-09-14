package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A quoted excerpt must say when it cut. The briefing it lands in is what a
// session uses to decide what went wrong, and an excerpt that silently starts
// mid-log reads as the whole story.
func TestTailSaysWhenItCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verify.log")
	var builder strings.Builder
	for i := 0; i < 400; i++ {
		builder.WriteString("a line of output that is long enough to matter\n")
	}
	builder.WriteString("the cause is here\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0644); err != nil {
		t.Fatal(err)
	}

	excerpt := tail(path, 200)
	if !strings.Contains(excerpt, "the cause is here") {
		t.Fatalf("the end of the log is missing:\n%s", excerpt)
	}
	if !strings.Contains(excerpt, "earlier output omitted") {
		t.Fatalf("a truncated excerpt does not say it was truncated:\n%s", excerpt)
	}
	if strings.HasPrefix(excerpt, "a line") || strings.Contains(excerpt, "\nne of output") {
		t.Fatalf("the excerpt starts mid-line:\n%s", excerpt)
	}
}

// A log shorter than the limit is quoted whole, with nothing claiming it was cut.
func TestTailQuotesAShortLogWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verify.log")
	if err := os.WriteFile(path, []byte("canonical check failed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if excerpt := tail(path, 4096); excerpt != "canonical check failed" {
		t.Fatalf("excerpt = %q", excerpt)
	}
}
