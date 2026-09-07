package repository

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateStory(t *testing.T) {
	root := t.TempDir()
	stories := filepath.Join(root, "specs", "stories")
	if err := os.MkdirAll(stories, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stories, "a.md"), []byte("story"), 0644); err != nil {
		t.Fatal(err)
	}
	story, err := ValidateStory(root, "specs/stories/a.md")
	if err != nil || story != "specs/stories/a.md" {
		t.Fatalf("story=%q err=%v", story, err)
	}
	for _, reference := range []string{"specs/stories/missing.md", "../outside.md", "/tmp/outside.md"} {
		if _, err := ValidateStory(root, reference); err == nil {
			t.Fatalf("accepted %q", reference)
		}
	}
	outside := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(stories, "escape.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStory(root, "specs/stories/escape.md"); err == nil {
		t.Fatal("accepted symlink escape")
	}
	linkedRoot, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "stories"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "stories", "a.md"), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(linkedRoot, "specs")); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStory(linkedRoot, "specs/stories/a.md"); err == nil {
		t.Fatal("accepted symlinked story directory outside repository")
	}
}
