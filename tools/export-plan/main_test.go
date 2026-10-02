package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func exportFixture(t *testing.T, statePath string) (outDir, report string) {
	t.Helper()
	outDir = filepath.Join(t.TempDir(), "plans")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--state", statePath, "--out", outDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("export-plan exited %d: %s", code, stderr.String())
	}
	return outDir, stdout.String()
}

func readPlan(t *testing.T, path string) plan {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("%s is not a Goal Plan: %v", path, err)
	}
	return p
}

func TestExportKeepsOnlyUnfinishedWorkOfLiveGoals(t *testing.T) {
	outDir, report := exportFixture(t, filepath.Join("testdata", "v18-state.json"))

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	// COMPLETED, CANCELLED and the Goal whose work is all finished produce nothing.
	if want := []string{"billing.json", "fast.lane.json", "paused.json"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}

	billing := readPlan(t, filepath.Join(outDir, "billing.json"))
	wantBilling := plan{
		Goal: planGoal{ID: "billing", Title: "Billing sync", Description: "Keep invoices in step with the ledger", RequireApproval: true},
		Nodes: []node{
			// WI-001 is DONE: dropped, and the edges that pointed at it are gone.
			{ID: "WI-002", Story: "specs/stories/b.md", DependsOn: []string{}},
			{ID: "WI-003", Story: "specs/stories/c.md", DependsOn: []string{"WI-002"}},
		},
	}
	if !reflect.DeepEqual(billing, wantBilling) {
		t.Errorf("billing plan = %#v\nwant          %#v", billing, wantBilling)
	}

	fast := readPlan(t, filepath.Join(outDir, "fast.lane.json"))
	wantFast := plan{
		Goal: planGoal{ID: "fast.lane", Title: "Fast lane", RequireApproval: false},
		Nodes: []node{
			// WI-004 is VERIFIED, which counts as finished.
			{ID: "WI-005", Story: "specs/stories/b.md", DependsOn: []string{}},
			{ID: "WI-006", Story: "specs/stories/c.md", DependsOn: []string{"WI-005"}},
		},
	}
	if !reflect.DeepEqual(fast, wantFast) {
		t.Errorf("fast.lane plan = %#v\nwant            %#v", fast, wantFast)
	}

	paused := readPlan(t, filepath.Join(outDir, "paused.json"))
	if paused.Goal.ID != "paused" || !paused.Goal.RequireApproval || len(paused.Nodes) != 1 || paused.Nodes[0].ID != "WI-007" || paused.Nodes[0].DependsOn == nil {
		t.Errorf("paused plan = %#v", paused)
	}

	for _, want := range []string{
		"billing.json: 2 nodes", "1 finished", "1 work items were RUNNING",
		"paused.json: 1 nodes", "was BLOCKED",
		"goal all-built: no unfinished work; skipped",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	for _, unwanted := range []string{"finished.json", "dropped.json", "all-built.json"} {
		if strings.Contains(report, unwanted) {
			t.Errorf("report mentions %q:\n%s", unwanted, report)
		}
	}
}

// A plan holds IDs and stories only, never an old Work Item's lifecycle.
func TestExportedPlansUseOnlyThePlanFields(t *testing.T) {
	outDir, _ := exportFixture(t, filepath.Join("testdata", "v18-state.json"))
	raw, err := os.ReadFile(filepath.Join(outDir, "billing.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var p plan
	if err := decoder.Decode(&p); err != nil {
		t.Fatalf("exported plan carries fields outside the Goal Plan format: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	if len(generic) != 2 {
		t.Fatalf("top-level keys = %v, want goal and nodes", generic)
	}
}

func TestExportRefusesStateItCannotOrShouldNotRead(t *testing.T) {
	write := func(contents string) string {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := map[string]struct {
		state string
		want  string
	}{
		"current schema":    {`{"schema_version":19,"goals":[],"work_items":[]}`, "nothing to export"},
		"newer schema":      {`{"schema_version":20,"goals":[],"work_items":[]}`, "nothing to export"},
		"no schema version": {`{"goals":[]}`, "schema_version"},
		"not json":          {`{`, "read"},
		"bad goal id":       {`{"schema_version":18,"goals":[{"id":"has space","title":"T","status":"ACTIVE"}],"work_items":[{"id":"WI-001","goal_id":"has space","story_ref":"s","status":"READY"}]}`, `"has space"`},
		"bad item id":       {`{"schema_version":18,"goals":[{"id":"g","title":"T","status":"ACTIVE"}],"work_items":[{"id":"bad id","goal_id":"g","story_ref":"s","status":"READY"}]}`, `"bad id"`},
	}
	for name, test := range cases {
		outDir := filepath.Join(t.TempDir(), "plans")
		var stdout, stderr bytes.Buffer
		code := run([]string{"--state", write(test.state), "--out", outDir}, &stdout, &stderr)
		if code == 0 || !strings.Contains(stderr.String(), test.want) {
			t.Errorf("%s: exit %d, stderr %q; want a failure mentioning %q", name, code, stderr.String(), test.want)
		}
		if _, err := os.Stat(outDir); err == nil {
			t.Errorf("%s: wrote an output directory despite the failure", name)
		}
	}
}

func TestExportTreatsAMissingReviewPolicyAsApprovalRequired(t *testing.T) {
	// State from before Goals had a review policy: the old behaviour was review
	// per Work Item, so the plan must keep requiring approval.
	path := filepath.Join(t.TempDir(), "state.json")
	state := `{"schema_version":3,"goals":[{"id":"old","title":"Old","status":"ACTIVE"}],"work_items":[{"id":"WI-001","goal_id":"old","story_ref":"specs/stories/a.md","status":"READY"}]}`
	if err := os.WriteFile(path, []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	outDir, _ := exportFixture(t, path)
	if p := readPlan(t, filepath.Join(outDir, "old.json")); !p.Goal.RequireApproval {
		t.Fatalf("plan = %#v, want require_approval true", p)
	}
}

func TestUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: export-plan --state") {
		t.Fatalf("no arguments: exit %d, stderr %q", code, stderr.String())
	}
}
