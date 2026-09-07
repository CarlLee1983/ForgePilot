package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ValidateStory(root, reference string) (string, error) {
	if reference == "" || filepath.IsAbs(reference) {
		return "", fmt.Errorf("story reference must be a repository-relative path under specs/stories")
	}
	for _, part := range strings.FieldsFunc(filepath.ToSlash(reference), func(r rune) bool { return r == '/' }) {
		if part == ".." {
			return "", fmt.Errorf("story reference must not traverse paths")
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	base, err := filepath.EvalSymlinks(filepath.Join(root, "specs", "stories"))
	if err != nil {
		return "", fmt.Errorf("resolve story directory: %w", err)
	}
	if !within(resolvedRoot, base) {
		return "", fmt.Errorf("story directory must remain inside the repository")
	}
	candidate := filepath.Join(root, reference)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("story reference %q does not exist", reference)
	}
	if !within(base, resolved) {
		return "", fmt.Errorf("story reference must remain under specs/stories")
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
