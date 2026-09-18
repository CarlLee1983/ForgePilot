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

// StoryReadinessFiles are raw, contained bytes. The repository package does
// not decode the sidecar or interpret either Markdown source.
type StoryReadinessFiles struct {
	Sidecar           []byte
	StoryMD           []byte
	AcceptanceMD      []byte
	StoryMDError      error
	AcceptanceMDError error
}

// StoryReadinessFileError identifies the fixed input that could not be read.
// The app maps this transport fact to its typed readiness report; repository
// deliberately makes no semantic classification itself.
type StoryReadinessFileError struct {
	Name string
	Err  error
}

func (err *StoryReadinessFileError) Error() string {
	return fmt.Sprintf("read %s: %v", err.Name, err.Err)
}
func (err *StoryReadinessFileError) Unwrap() error { return err.Err }

// ReadStoryReadinessFiles reads the fixed v1 readiness inputs from one Story
// directory. A sidecar or source must be a regular file directly in that
// directory; accepting a symlink would make the contract's named-file binding
// depend on an indirection the caller did not authorize.
func ReadStoryReadinessFiles(root, reference string) (StoryReadinessFiles, error) {
	normalized, err := ValidateStory(root, reference)
	if err != nil {
		return StoryReadinessFiles{}, err
	}
	storyDirectory := filepath.Join(root, normalized)
	info, err := os.Stat(storyDirectory)
	if err != nil {
		return StoryReadinessFiles{}, fmt.Errorf("read Story readiness directory: %w", err)
	}
	if !info.IsDir() {
		return StoryReadinessFiles{}, &StoryReadinessFileError{Name: "readiness.json", Err: errors.New("Story readiness contract requires a Story directory")}
	}
	read := func(name string) ([]byte, error) {
		path := filepath.Join(storyDirectory, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("read %s: not a regular contained file", name)
		}
		return os.ReadFile(path)
	}
	sidecar, err := read("readiness.json")
	if err != nil {
		return StoryReadinessFiles{}, &StoryReadinessFileError{Name: "readiness.json", Err: err}
	}
	story, storyErr := read("story.md")
	acceptance, acceptanceErr := read("acceptance.md")
	files := StoryReadinessFiles{Sidecar: sidecar, StoryMD: story, AcceptanceMD: acceptance}
	if storyErr != nil {
		files.StoryMDError = storyErr
	}
	if acceptanceErr != nil {
		files.AcceptanceMDError = acceptanceErr
	}
	return files, nil
}

// ContainedRegularFile reports whether path names a regular, non-symlink file
// under root. It is a filesystem fact for app orchestration, not a readiness
// decision.
func ContainedRegularFile(root, path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(filepath.ToSlash(path), "../") {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	candidate := filepath.Join(resolvedRoot, path)
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	return err == nil && within(resolvedRoot, resolved)
}

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
		return "", errors.New("specs/stories does not exist in this repository; ForgePilot expects PraxisBound Story files to live under specs/stories")
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
