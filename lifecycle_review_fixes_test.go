package forgepilot_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// A Gate can be opened while a check is running. The PASS is then recorded but
// cannot complete the work (the same criterion approve applies), and verify says
// so. Before this the completion was attempted anyway and the whole transaction
// was refused at save time, losing the Evidence.
func TestGateOpenedMidVerificationKeepsThePassWithoutCompleting(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	createGoal(t, binary, root, "queue", "Queue", false)
	addWork(t, binary, root, "queue", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	writeVerify(t, root, "verify:\n\t@sleep 2\n")

	running := exec.Command(binary, "verify", "WI-001")
	running.Dir = root
	var output bytes.Buffer
	running.Stdout, running.Stderr = &output, &output
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		state, err := storage.Load(root)
		if err == nil && state.WorkItemStatus("WI-001") == work.Verifying {
			break
		}
		if time.Now().After(deadline) {
			_ = running.Process.Kill()
			t.Fatal("verification never entered VERIFYING")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001", "--question", "Proceed?", "--option", "yes", "--option", "no")
	if err := running.Wait(); err != nil {
		t.Fatalf("verify failed instead of recording its PASS: %v: %s", err, output.String())
	}

	if !strings.Contains(output.String(), "PASS recorded but WI-001 was not completed") || !strings.Contains(output.String(), "GATE-001") {
		t.Fatalf("verify output does not explain the PASS was not completed:\n%s", output.String())
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatalf("state no longer loads: %v", err)
	}
	if latest, ok := state.LatestVerification("WI-001"); !ok || latest.Result != work.Pass {
		t.Fatalf("the PASS Evidence was lost: %#v, %v", latest, ok)
	}
	wantStatuses(t, root, "queue", map[string]string{"WI-001": "RUNNING"}, work.GoalActive)

	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "yes")
	mustRun(t, binary, root, "verify", "WI-001")
	wantStatuses(t, root, "queue", map[string]string{"WI-001": "DONE"}, work.GoalCompleted)
}

// Rejecting stops work and needs neither a clean worktree nor a current
// Candidate; only approval, which completes work, does.
func TestRejectIsNotHeldBackByADirtyWorktreeOrAStaleSnapshot(t *testing.T) {
	t.Run("dirty worktree on a COMMIT", func(t *testing.T) {
		root, binary := fixture(t)
		reviewable(t, binary, root)
		if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("uncommitted\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if output, err := command(binary, root, "review", "approve", "WI-001"); err == nil {
			t.Fatalf("approved on a dirty worktree: %s", output)
		}
		output, err := command(binary, root, "review", "reject", "WI-001", "--reason", "not yet")
		if err != nil || !strings.Contains(output, "WI-001 RUNNING") {
			t.Fatalf("reject on a dirty worktree = %q, %v", output, err)
		}
	})

	t.Run("stale SNAPSHOT", func(t *testing.T) {
		root, binary := fixture(t)
		writeVerify(t, root, passingVerify)
		mustRun(t, binary, root, "init")
		createGoal(t, binary, root, "queue", "Queue", true)
		addWork(t, binary, root, "queue", "specs/stories/a.md")
		mustRun(t, binary, root, "start", "WI-001")
		mustRun(t, binary, root, "verify", "WI-001", "--snapshot")
		wantStatuses(t, root, "queue", map[string]string{"WI-001": "REVIEW"}, work.GoalActive)
		if err := os.WriteFile(filepath.Join(root, "changed.txt"), []byte("new\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if output, err := command(binary, root, "review", "approve", "WI-001"); err == nil || !strings.Contains(output, "stale") {
			t.Fatalf("approve of a stale snapshot = %q, %v", output, err)
		}
		output, err := command(binary, root, "review", "reject", "WI-001", "--reason", "not yet")
		if err != nil || !strings.Contains(output, "WI-001 RUNNING") {
			t.Fatalf("reject of a stale snapshot = %q, %v", output, err)
		}
		state, loadErr := storage.Load(root)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		review, ok := state.LatestReview("WI-001")
		verification, _ := state.LatestVerification("WI-001")
		if !ok || review.Result != work.Rejected || review.CandidateDigest != verification.CandidateDigest {
			t.Fatalf("the rejection is not recorded against the PASS it judged: %#v", review)
		}
	})
}
