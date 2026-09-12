package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureSnapshotAndInspectSnapshotPreserveWorktree(t *testing.T) {
	root := newSnapshotRepository(t)
	writeSnapshotFile(t, root, "staged.txt", "base\n")
	writeSnapshotFile(t, root, "unstaged.txt", "base\n")
	writeSnapshotFile(t, root, "deleted.txt", "base\n")
	writeSnapshotFile(t, root, "tracked-ignored.txt", "base\n")
	gitSnapshot(t, root, "add", "staged.txt", "unstaged.txt", "deleted.txt", "tracked-ignored.txt")
	gitSnapshot(t, root, "commit", "-m", "base")

	writeSnapshotFile(t, root, "staged.txt", "staged\n")
	gitSnapshot(t, root, "add", "staged.txt")
	writeSnapshotFile(t, root, "unstaged.txt", "unstaged\n")
	writeSnapshotFile(t, root, "untracked.txt", "untracked\n")
	writeSnapshotFile(t, root, "ignored.txt", "ignored\n")
	writeSnapshotFile(t, root, "tracked-ignored.txt", "tracked but now ignored\n")
	writeSnapshotFile(t, root, ".gitignore", "ignored.txt\ntracked-ignored.txt\n")
	gitSnapshot(t, root, "add", ".gitignore")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}

	before := repositorySurface(t, root)
	createdAt := time.Date(2026, time.September, 12, 8, 0, 0, 0, time.UTC)
	captured, err := CaptureSnapshot(root, "WI-001", createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if captured.BaseRevision == "" || captured.Revision == "" || !strings.HasPrefix(captured.Digest, "sha256:") {
		t.Fatalf("capture = %#v", captured)
	}
	if !strings.HasPrefix(captured.Ref, "refs/forgepilot/snapshots/") {
		t.Fatalf("capture ref = %q", captured.Ref)
	}
	if after := repositorySurface(t, root); after != before {
		t.Fatalf("capture changed worktree surface\nwant:\n%s\ngot:\n%s", before, after)
	}
	changes := gitSnapshot(t, root, "show", "--format=", "--name-status", captured.Revision)
	for _, want := range []string{"M\tstaged.txt", "M\tunstaged.txt", "M\ttracked-ignored.txt", "D\tdeleted.txt", "A\t.gitignore", "A\tuntracked.txt"} {
		if !strings.Contains(changes, want) {
			t.Fatalf("snapshot changes = %q, want %q", changes, want)
		}
	}
	for _, want := range []string{"staged.txt:staged\n", "unstaged.txt:unstaged\n", "tracked-ignored.txt:tracked but now ignored\n"} {
		parts := strings.SplitN(want, ":", 2)
		if got := gitSnapshot(t, root, "show", captured.Revision+":"+parts[0]); got != parts[1] {
			t.Fatalf("snapshot %s = %q, want %q", parts[0], got, parts[1])
		}
	}
	for _, path := range strings.Fields(gitSnapshot(t, root, "ls-tree", "-r", "--name-only", captured.Revision)) {
		if path == "ignored.txt" {
			t.Fatal("snapshot contains ignored untracked file")
		}
	}
	if got := strings.TrimSpace(gitSnapshot(t, root, "rev-parse", captured.Ref)); got != captured.Revision {
		t.Fatalf("snapshot ref = %q, want %q", got, captured.Revision)
	}
	if repeated, err := CaptureSnapshot(root, "WI-001", createdAt); err != nil || repeated != captured {
		t.Fatalf("repeat capture = %#v, %v; want %#v", repeated, err, captured)
	}
	checkout := filepath.Join(t.TempDir(), "snapshot")
	gitSnapshot(t, root, "worktree", "add", "--detach", checkout, captured.Ref)
	t.Cleanup(func() { gitSnapshot(t, root, "worktree", "remove", "--force", checkout) })
	if _, err := os.Stat(filepath.Join(checkout, "deleted.txt")); !os.IsNotExist(err) {
		t.Fatalf("checkout restored deleted tracked file: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(checkout, "untracked.txt")); err != nil || string(got) != "untracked\n" {
		t.Fatalf("checkout untracked file = %q, %v", got, err)
	}

	if err := os.Chtimes(filepath.Join(root, "staged.txt"), createdAt.Add(24*time.Hour), createdAt.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	beforeInspect := repositorySurface(t, root)
	refsBeforeInspect := gitSnapshot(t, root, "for-each-ref", "--format=%(refname) %(objectname)", "refs/forgepilot/snapshots")
	objectsBeforeInspect := directorySurface(t, filepath.Join(root, ".git", "objects"))
	inspected, err := InspectSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.BaseRevision != captured.BaseRevision || inspected.Digest != captured.Digest {
		t.Fatalf("inspect = %#v, capture = %#v", inspected, captured)
	}
	if inspected.Ref != "" {
		t.Fatalf("inspect created ref %q", inspected.Ref)
	}
	again, err := InspectSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if again != inspected {
		t.Fatalf("inspect changed between reads: first %#v, second %#v", inspected, again)
	}
	if err := os.Chmod(filepath.Join(root, "unstaged.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	modeChanged, err := InspectSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if modeChanged.Digest == inspected.Digest {
		t.Fatal("candidate digest ignored executable mode change")
	}
	if err := os.Chmod(filepath.Join(root, "unstaged.txt"), 0644); err != nil {
		t.Fatal(err)
	}
	if refsAfterInspect := gitSnapshot(t, root, "for-each-ref", "--format=%(refname) %(objectname)", "refs/forgepilot/snapshots"); refsAfterInspect != refsBeforeInspect {
		t.Fatalf("inspect changed snapshot refs\nwant %q\ngot  %q", refsBeforeInspect, refsAfterInspect)
	}
	if objectsAfterInspect := directorySurface(t, filepath.Join(root, ".git", "objects")); objectsAfterInspect != objectsBeforeInspect {
		t.Fatal("inspect wrote to the repository object database")
	}
	if after := repositorySurface(t, root); after != beforeInspect {
		t.Fatalf("inspect changed worktree surface\nwant:\n%s\ngot:\n%s", beforeInspect, after)
	}
}

func directorySurface(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, relative+"\x00"+string(content))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return strings.Join(entries, "\x00")
}

func TestCaptureSnapshotDigestBindsBaseRevision(t *testing.T) {
	createdAt := time.Date(2026, time.September, 12, 8, 0, 0, 0, time.UTC)
	first := newSnapshotRepository(t)
	writeSnapshotFile(t, first, "file.txt", "first base\n")
	gitSnapshot(t, first, "add", "file.txt")
	gitSnapshot(t, first, "commit", "-m", "first base")
	writeSnapshotFile(t, first, "file.txt", "same final contents\n")
	firstSnapshot, err := CaptureSnapshot(first, "WI-001", createdAt)
	if err != nil {
		t.Fatal(err)
	}

	second := newSnapshotRepository(t)
	writeSnapshotFile(t, second, "file.txt", "second base\n")
	gitSnapshot(t, second, "add", "file.txt")
	gitSnapshot(t, second, "commit", "-m", "second base")
	writeSnapshotFile(t, second, "file.txt", "same final contents\n")
	secondSnapshot, err := CaptureSnapshot(second, "WI-001", createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if firstSnapshot.BaseRevision == secondSnapshot.BaseRevision {
		t.Fatal("fixtures unexpectedly have the same base revision")
	}
	if firstTree, secondTree := gitSnapshot(t, first, "rev-parse", firstSnapshot.Revision+"^{tree}"), gitSnapshot(t, second, "rev-parse", secondSnapshot.Revision+"^{tree}"); firstTree != secondTree {
		t.Fatalf("final trees differ: %q and %q", firstTree, secondTree)
	}
	if firstSnapshot.Digest == secondSnapshot.Digest {
		t.Fatalf("digest %q does not bind base revision", firstSnapshot.Digest)
	}
}

func TestSnapshotOverridesInheritedGitIndex(t *testing.T) {
	root := newSnapshotRepository(t)
	writeSnapshotFile(t, root, "file.txt", "base\n")
	gitSnapshot(t, root, "add", "file.txt")
	gitSnapshot(t, root, "commit", "-m", "base")
	writeSnapshotFile(t, root, "file.txt", "working\n")
	realIndex := filepath.Join(root, ".git", "index")
	before, err := os.ReadFile(realIndex)
	if err != nil {
		t.Fatal(err)
	}
	foreignIndex := filepath.Join(t.TempDir(), "foreign-index")
	if err := os.WriteFile(foreignIndex, []byte("not a git index"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_INDEX_FILE", foreignIndex)
	if _, err := CaptureSnapshot(root, "WI-001", time.Date(2026, time.September, 12, 8, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("capture with inherited GIT_INDEX_FILE: %v", err)
	}
	if _, err := InspectSnapshot(root); err != nil {
		t.Fatalf("inspect with inherited GIT_INDEX_FILE: %v", err)
	}
	after, err := os.ReadFile(realIndex)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("snapshot changed the real index despite inherited GIT_INDEX_FILE")
	}
}

func newSnapshotRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitSnapshot(t, root, "init")
	gitSnapshot(t, root, "config", "user.name", "Test User")
	gitSnapshot(t, root, "config", "user.email", "test@example.com")
	return root
}

func writeSnapshotFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func gitSnapshot(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	output, err := git(root, nil, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func repositorySurface(t *testing.T, root string) string {
	t.Helper()
	status := gitSnapshot(t, root, "status", "--porcelain=v1", "--untracked-files=all")
	diff := gitSnapshot(t, root, "diff", "--binary")
	cachedDiff := gitSnapshot(t, root, "diff", "--cached", "--binary")
	head := gitSnapshot(t, root, "rev-parse", "HEAD")
	branch := gitSnapshot(t, root, "branch", "--show-current")
	index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(root, "staged.txt"))
	if err != nil {
		t.Fatal(err)
	}
	unstaged, err := os.ReadFile(filepath.Join(root, "unstaged.txt"))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := os.ReadFile(filepath.Join(root, "deleted.txt"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	untracked, err := os.ReadFile(filepath.Join(root, "untracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := os.ReadFile(filepath.Join(root, "ignored.txt"))
	if err != nil {
		t.Fatal(err)
	}
	trackedIgnored, err := os.ReadFile(filepath.Join(root, "tracked-ignored.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{
		status, diff, cachedDiff, head, branch,
		string(index), string(staged), string(unstaged), string(deleted), string(untracked), string(ignored), string(trackedIgnored),
	}, "\x00")
}
