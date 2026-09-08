package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// storyLocationRule is what a story reference is rejected for whenever the
// problem is the user's input rather than the repository's setup: escaping
// specs/stories, or pointing at something that isn't there. Both are the same
// violation from the caller's side — fix the path — so they share one message
// instead of leaking which internal check happened to catch it.
const storyLocationRule = "story reference must be located under specs/stories"

func ValidateStory(root, reference string) (string, error) {
	if reference == "" || filepath.IsAbs(reference) {
		return "", fmt.Errorf("%s: reference must be repository-relative, got %q", storyLocationRule, reference)
	}
	for _, part := range strings.FieldsFunc(filepath.ToSlash(reference), func(r rune) bool { return r == '/' }) {
		if part == ".." {
			return "", fmt.Errorf("%s: reference must not traverse with \"..\", got %q", storyLocationRule, reference)
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	base, err := filepath.EvalSymlinks(filepath.Join(root, "specs", "stories"))
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("specs/stories does not exist in this repository; ForgePilot expects ForgeFlow Story files to live under specs/stories")
	}
	if err != nil {
		return "", fmt.Errorf("resolve story directory: %w", err)
	}
	if !within(resolvedRoot, base) {
		return "", fmt.Errorf("story directory must remain inside the repository")
	}
	candidate := filepath.Join(root, reference)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%s: %q", storyLocationRule, reference)
	}
	if !within(base, resolved) {
		return "", fmt.Errorf("%s: %q", storyLocationRule, reference)
	}
	info, err := os.Stat(resolved)
	if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
		return "", fmt.Errorf("story reference %q is not a file or directory", reference)
	}
	normalized, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(normalized), nil
}

func within(base, path string) bool {
	relative, err := filepath.Rel(base, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
