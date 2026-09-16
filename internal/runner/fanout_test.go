package runner

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestVerificationEvidenceIDsIncludesEveryFanoutRecord(t *testing.T) {
	reclaimed := work.Evidence{ID: "EV-001"}
	result := app.VerifyResult{
		Reclaimed:    &reclaimed,
		ReclaimedSet: []work.Evidence{reclaimed},
		Evidence:     work.Evidence{ID: "EV-002"},
		EvidenceSet: []work.Evidence{
			{ID: "EV-002"}, {ID: "EV-003"}, {ID: "EV-004"},
		},
		HasEvidence: true,
	}
	if got, want := verificationEvidenceIDs(result), []string{"EV-001", "EV-002", "EV-003", "EV-004"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Evidence IDs = %v, want %v", got, want)
	}
}

func TestLastFailureFindsNewRunLogByExactVerificationRunID(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".forgepilot", "logs")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "VR-1000-deadbeef-when-token.log"), []byte("wrong\n"), 0644); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(directory, "VR-100-deadbeef-when-token.log")
	if err := os.WriteFile(wantPath, []byte("right failure\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := Runner{record: &Record{Workspace: root}, options: Options{Excerpt: 1024}}
	state := work.State{Evidence: []work.Evidence{{
		ID: "EV-100", Type: work.VerificationEvidence, WorkItemID: "WI-001",
		Revision: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", Result: work.Fail,
		VerificationRunID: "VR-100",
	}}}
	excerpt, path, err := runner.lastFailure(&state, "WI-001")
	if err != nil {
		t.Fatal(err)
	}
	if excerpt != "right failure" || path != wantPath {
		t.Fatalf("failure excerpt/path = %q, %q; want exact VR log %q", excerpt, path, wantPath)
	}
}

func TestLastFailureUsesLegacyWorkItemRevisionLookupOnlyForMigratedRuns(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".forgepilot", "logs")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(directory, "WI-007-abcdef012345-20260101.log")
	if err := os.WriteFile(wantPath, []byte("legacy failure\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := Runner{record: &Record{Workspace: root}, options: Options{Excerpt: 1024}}
	state := work.State{Evidence: []work.Evidence{{
		ID: "EV-007", Type: work.VerificationEvidence, WorkItemID: "WI-007",
		Revision: "abcdef0123456789abcdef0123456789abcdef01", Result: work.Fail,
		VerificationRunID: "LVR-007",
	}}}
	excerpt, path, err := runner.lastFailure(&state, "WI-007")
	if err != nil {
		t.Fatal(err)
	}
	if excerpt != "legacy failure" || path != wantPath {
		t.Fatalf("legacy failure excerpt/path = %q, %q; want %q", excerpt, path, wantPath)
	}
}

func TestLastFailureReportsMissingOrAmbiguousNewRunLog(t *testing.T) {
	root := t.TempDir()
	runner := Runner{record: &Record{Workspace: root}, options: Options{Excerpt: 1024}}
	state := work.State{Evidence: []work.Evidence{{
		ID: "EV-001", Type: work.VerificationEvidence, WorkItemID: "WI-001",
		Revision: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", Result: work.Fail,
		VerificationRunID: "VR-001",
	}}}
	if _, _, err := runner.lastFailure(&state, "WI-001"); !errors.Is(err, repository.ErrVerificationLogNotFound) {
		t.Fatalf("missing log error = %v, want ErrVerificationLogNotFound", err)
	}
	directory := filepath.Join(root, ".forgepilot", "logs")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VR-001-a.log", "VR-001-b.log"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("failure\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := runner.lastFailure(&state, "WI-001"); !errors.Is(err, repository.ErrVerificationLogAmbiguous) {
		t.Fatalf("ambiguous log error = %v, want ErrVerificationLogAmbiguous", err)
	}
}

func TestLastFailureReportsMissingOrAmbiguousLegacyRunLog(t *testing.T) {
	root := t.TempDir()
	runner := Runner{record: &Record{Workspace: root}, options: Options{Excerpt: 1024}}
	state := work.State{Evidence: []work.Evidence{{
		ID: "EV-007", Type: work.VerificationEvidence, WorkItemID: "WI-007",
		Revision: "abcdef0123456789abcdef0123456789abcdef01", Result: work.Fail,
		VerificationRunID: "LVR-007",
	}}}
	if _, _, err := runner.lastFailure(&state, "WI-007"); !errors.Is(err, repository.ErrVerificationLogNotFound) {
		t.Fatalf("missing legacy log error = %v, want ErrVerificationLogNotFound", err)
	}
	directory := filepath.Join(root, ".forgepilot", "logs")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"WI-007-abcdef012345-20260101.log",
		"WI-007-abcdef012345-20260102.log",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("failure\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := runner.lastFailure(&state, "WI-007"); !errors.Is(err, repository.ErrVerificationLogAmbiguous) {
		t.Fatalf("ambiguous legacy log error = %v, want ErrVerificationLogAmbiguous", err)
	}
}
