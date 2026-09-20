package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestGoalPreflightEmitsVersionedProjectionWithoutWritesOrSubprocesses(t *testing.T) {
	root := preflightCLIFixture(t)
	spyDir := t.TempDir()
	logPath := filepath.Join(spyDir, "process.log")
	spy := []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$PREFLIGHT_PROCESS_LOG\"\nexit 99\n")
	for _, name := range []string{"git", "make", "ps", "go", "node", "python3", "codex", "mise", "asdf", "sh", "command"} {
		if err := os.WriteFile(filepath.Join(spyDir, name), spy, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", spyDir)
	t.Setenv("TMPDIR", spyDir)
	t.Setenv("PREFLIGHT_PROCESS_LOG", logPath)

	beforeRoot := preflightCLITree(t, root)
	beforeSpy := preflightCLITree(t, spyDir)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"goal", "preflight", "--request", "preflight-request.json", "--json"}, root, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q, stdout = %q", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var projection app.GoalPreflightProjection
	if err := json.Unmarshal(stdout.Bytes(), &projection); err != nil {
		t.Fatalf("stdout is not one JSON projection: %v; output = %q", err, stdout.String())
	}
	if projection.Version != app.GoalPreflightVersion || len(projection.Diagnostics) != 0 {
		t.Fatalf("projection = %#v", projection)
	}
	if projection.Goal.ID != "goal" || projection.Facts["registration"].Status != "observed" {
		t.Fatalf("projection = %#v", projection)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("subprocess spy log exists or could not be checked: %v", err)
	}
	if after := preflightCLITree(t, root); !reflect.DeepEqual(beforeRoot, after) {
		t.Fatalf("repository files changed during preflight: before=%v after=%v", beforeRoot, after)
	}
	if after := preflightCLITree(t, spyDir); !reflect.DeepEqual(beforeSpy, after) {
		t.Fatalf("temporary files changed during preflight: before=%v after=%v", beforeSpy, after)
	}
}

func preflightCLIFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	repositoryRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var first, second work.Item
	now := time.Unix(0, 0).UTC()
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoal("goal", "Goal", "", repositoryRoot, now); err != nil {
			return err
		}
		first, err = state.AddWork("goal", "specs/stories/EX-001-first", nil, now)
		if err != nil {
			return err
		}
		second, err = state.AddWork("goal", "specs/stories/EX-001-first", []string{first.ID}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fixtureRoot := filepath.Join("..", "app", "testdata", "goal-plan-artifacts", "v1", "valid")
	copyPreflightFixtureTree(t, filepath.Join(fixtureRoot, "repository"), root)
	copyPreflightFixtureFile(t, filepath.Join(fixtureRoot, "goal-plan-manifest.json"), filepath.Join(root, "manifest.json"))
	copyPreflightFixtureFile(t, filepath.Join(fixtureRoot, "plan-coverage-review.json"), filepath.Join(root, "review.json"))
	request := app.GoalPreflightRequest{
		FormatVersion: "forgepilot.goal-preflight-request/v1", GoalID: "goal",
		ManifestPath: "manifest.json", CoverageReviewPath: "review.json",
		NodeMappings: []app.GoalPlanNodeMapping{
			{PlanNodeRef: "node-001", WorkItemID: first.ID},
			{PlanNodeRef: "node-002", WorkItemID: second.ID},
		},
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "preflight-request.json"), requestBytes, 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyPreflightFixtureTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyPreflightFixtureFile(t *testing.T, source, destination string) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, body, 0644); err != nil {
		t.Fatal(err)
	}
}

func preflightCLIDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func preflightCLITree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%s|%s", info.Mode(), info.ModTime().UTC().Format(time.RFC3339Nano))
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += "|" + preflightCLIDigest(body)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += "|" + target
		}
		files[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
