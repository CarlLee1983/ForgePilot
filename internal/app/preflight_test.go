package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestPreflightGoalPlanValidTopologyAndFacts(t *testing.T) {
	for _, topology := range []struct {
		name  string
		edges [][2]string
	}{
		{"chain", [][2]string{{"a", "b"}, {"b", "c"}}},
		{"diamond", [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}},
		{"fanin", [][2]string{{"a", "c"}, {"b", "c"}}},
	} {
		t.Run(topology.name, func(t *testing.T) {
			root, request := preflightFixture(t, topology.edges, false)
			before := preflightStateBytes(t, root)
			projection, err := PreflightGoalPlan(context.Background(), root, request)
			if err != nil {
				t.Fatal(err)
			}
			if projection.Version != GoalPreflightVersion || len(projection.Diagnostics) != 0 {
				t.Fatalf("projection = %#v", projection)
			}
			if projection.Goal.ID != request.GoalID {
				t.Fatalf("goal projection = %#v", projection.Goal)
			}
			encoded, err := json.Marshal(projection)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(root)) {
				t.Fatal("preflight projection exposed the repository's absolute path")
			}
			for _, name := range []string{"goal", "manifest", "declaration", "sources", "readinessContracts", "coverageReview", "registration"} {
				if projection.Facts[name].Status != "observed" {
					t.Fatalf("%s = %#v", name, projection.Facts[name])
				}
			}
			for _, name := range []string{"candidateFreshness", "workerState", "runtimeState", "nextAction"} {
				status := projection.Facts[name].Status
				if status != "unprobed" && status != "unavailable" {
					t.Fatalf("%s = %q", name, status)
				}
			}
			if after := preflightStateBytes(t, root); !bytes.Equal(before, after) {
				t.Fatal("preflight mutated state")
			}
		})
	}
}

func TestPreflightGoalPlanReportsFactStatusesOnFailure(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, root string, request *GoalPreflightRequest)
		want    map[string]string
	}{
		{
			name: "manifest unavailable",
			prepare: func(t *testing.T, root string, request *GoalPreflightRequest) {
				if err := os.Remove(filepath.Join(root, request.ManifestPath)); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string]string{"goal": "observed", "manifest": "unavailable", "coverageReview": "unprobed", "registration": "unprobed"},
		},
		{
			name: "coverage review unavailable",
			prepare: func(t *testing.T, root string, request *GoalPreflightRequest) {
				if err := os.Remove(filepath.Join(root, request.CoverageReviewPath)); err != nil {
					t.Fatal(err)
				}
			},
			want: map[string]string{"goal": "observed", "manifest": "observed", "declaration": "observed", "sources": "observed", "readinessContracts": "observed", "coverageReview": "unavailable", "registration": "unprobed"},
		},
		{
			name: "registration mismatch",
			prepare: func(t *testing.T, root string, request *GoalPreflightRequest) {
				request.NodeMappings[0].WorkItemID = "missing-work-item"
			},
			want: map[string]string{"goal": "observed", "manifest": "observed", "coverageReview": "observed", "registration": "unavailable"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
			test.prepare(t, root, &request)
			projection, err := PreflightGoalPlan(context.Background(), root, request)
			if err != nil {
				t.Fatal(err)
			}
			assertPreflightFactStatuses(t, projection, test.want)
		})
	}
}

func TestPreflightGoalPlanValidatesBoundDeclaration(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func([]byte) []byte
		wantCode string
	}{
		{
			name: "unsupported schema",
			mutate: func(body []byte) []byte {
				var declaration map[string]any
				if err := json.Unmarshal(body, &declaration); err != nil {
					t.Fatal(err)
				}
				declaration["schemaVersion"] = "2.0.0"
				updated, err := json.Marshal(declaration)
				if err != nil {
					t.Fatal(err)
				}
				return updated
			},
			wantCode: "unsupported-schema",
		},
		{
			name: "unknown field",
			mutate: func(body []byte) []byte {
				var declaration map[string]any
				if err := json.Unmarshal(body, &declaration); err != nil {
					t.Fatal(err)
				}
				declaration["unexpected"] = true
				updated, err := json.Marshal(declaration)
				if err != nil {
					t.Fatal(err)
				}
				return updated
			},
			wantCode: "malformed-artifact",
		},
		{
			name: "declaration topology differs from manifest",
			mutate: func(body []byte) []byte {
				var declaration map[string]any
				if err := json.Unmarshal(body, &declaration); err != nil {
					t.Fatal(err)
				}
				declaration["nodes"].([]any)[1].(map[string]any)["storyRef"] = "another-story"
				updated, err := json.Marshal(declaration)
				if err != nil {
					t.Fatal(err)
				}
				return updated
			},
			wantCode: "invalid-topology",
		},
		{
			name: "oversized declaration",
			mutate: func(body []byte) []byte {
				return append(bytes.TrimSpace(body), bytes.Repeat([]byte(" "), 1<<20)...)
			},
			wantCode: "malformed-artifact",
		},
		{
			name: "declaration exceeds JSON depth",
			mutate: func(body []byte) []byte {
				var declaration map[string]any
				if err := json.Unmarshal(body, &declaration); err != nil {
					t.Fatal(err)
				}
				var nested any = true
				for i := 0; i < 33; i++ {
					nested = []any{nested}
				}
				declaration["unexpected"] = nested
				updated, err := json.Marshal(declaration)
				if err != nil {
					t.Fatal(err)
				}
				return updated
			},
			wantCode: "malformed-artifact",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
			declaration, err := os.ReadFile(filepath.Join(root, "declaration.json"))
			if err != nil {
				t.Fatal(err)
			}
			updated := test.mutate(declaration)
			if err := os.WriteFile(filepath.Join(root, "declaration.json"), updated, 0644); err != nil {
				t.Fatal(err)
			}
			mutatePreflightArtifacts(t, root, func(manifest map[string]any) {
				manifest["declaration"].(map[string]any)["sha256"] = testDigest(updated)
			}, nil)
			projection, err := PreflightGoalPlan(context.Background(), root, request)
			if err != nil {
				t.Fatal(err)
			}
			if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != test.wantCode {
				t.Fatalf("diagnostics = %#v", projection.Diagnostics)
			}
			assertPreflightFactStatuses(t, projection, map[string]string{
				"goal": "observed", "manifest": "observed", "declaration": "unavailable",
				"sources": "unprobed", "readinessContracts": "unprobed",
				"coverageReview": "unprobed", "registration": "unprobed",
			})
		})
	}
}

func assertPreflightFactStatuses(t *testing.T, projection GoalPreflightProjection, want map[string]string) {
	t.Helper()
	for name, fact := range projection.Facts {
		if fact.Status != "observed" && fact.Status != "unprobed" && fact.Status != "unavailable" {
			t.Errorf("%s has invalid status %q", name, fact.Status)
		}
	}
	for name, status := range want {
		if got := projection.Facts[name].Status; got != status {
			t.Errorf("%s status = %q, want %q", name, got, status)
		}
	}
}

func TestPreflightConsumesCanonicalPraxisBoundGoalPlanFixture(t *testing.T) {
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

	fixtureRoot := filepath.Join("testdata", "goal-plan-artifacts", "v1", "valid")
	copyFixtureTree(t, filepath.Join(fixtureRoot, "repository"), root)
	copyFixtureFile(t, filepath.Join(fixtureRoot, "goal-plan-manifest.json"), filepath.Join(root, "goal-plan-manifest.json"))
	copyFixtureFile(t, filepath.Join(fixtureRoot, "plan-coverage-review.json"), filepath.Join(root, "plan-coverage-review.json"))
	request := GoalPreflightRequest{
		FormatVersion:      goalPreflightRequestVersion,
		GoalID:             "goal",
		ManifestPath:       "goal-plan-manifest.json",
		CoverageReviewPath: "plan-coverage-review.json",
		NodeMappings: []GoalPlanNodeMapping{
			{PlanNodeRef: "node-001", WorkItemID: first.ID},
			{PlanNodeRef: "node-002", WorkItemID: second.ID},
		},
	}

	projection, err := PreflightGoalPlan(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 0 {
		t.Fatalf("canonical PraxisBound artifacts rejected: %#v", projection.Diagnostics)
	}
	if projection.Facts["registration"].Status != "observed" {
		t.Fatalf("registration = %#v", projection.Facts["registration"])
	}
}

func TestPraxisBoundGoalPlanFixturesMatchPublishedDigests(t *testing.T) {
	fixtureRoot := filepath.Join("testdata", "goal-plan-artifacts", "v1")
	body, err := os.ReadFile(filepath.Join(fixtureRoot, "fixtures.sha256.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		SchemaVersion string            `json:"schemaVersion"`
		SHA256        map[string]string `json:"sha256"`
	}
	if err := json.Unmarshal(body, &index); err != nil {
		t.Fatal(err)
	}
	if index.SchemaVersion != "1.0.0" || len(index.SHA256) == 0 {
		t.Fatalf("fixture digest index = %#v", index)
	}
	for path, want := range index.SHA256 {
		got, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read fixture %s: %v", path, err)
		}
		if testDigest(got) != want {
			t.Errorf("fixture %s digest = %s, want %s", path, testDigest(got), want)
		}
	}
}

func copyFixtureTree(t *testing.T, source, destination string) {
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

func copyFixtureFile(t *testing.T, source, destination string) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, body, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightGoalPlanSourceDriftDoesNotMutateState(t *testing.T) {
	root, request := preflightFixture(t, [][2]string{{"a", "b"}}, true)
	before, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	projection, err := PreflightGoalPlan(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "digest-mismatch" {
		t.Fatalf("diagnostics = %#v", projection.Diagnostics)
	}
	assertPreflightFactStatuses(t, projection, map[string]string{
		"goal": "observed", "manifest": "observed", "declaration": "observed",
		"sources": "unavailable", "readinessContracts": "unprobed",
		"coverageReview": "unprobed", "registration": "unprobed",
	})
	after, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("preflight mutated state")
	}
}

func TestPreflightGoalPlanReportsReadinessBindingProgress(t *testing.T) {
	root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
	mutatePreflightArtifacts(t, root, func(manifest map[string]any) {
		manifest["nodes"].([]any)[0].(map[string]any)["readinessContract"].(map[string]any)["sha256"] = strings.Repeat("0", 64)
	}, nil)
	projection, err := PreflightGoalPlan(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "digest-mismatch" {
		t.Fatalf("diagnostics = %#v", projection.Diagnostics)
	}
	assertPreflightFactStatuses(t, projection, map[string]string{
		"goal": "observed", "manifest": "observed", "declaration": "observed",
		"sources": "observed", "readinessContracts": "unavailable",
		"coverageReview": "unprobed", "registration": "unprobed",
	})
}

func TestPreflightGoalPlanRejectsSymlinkedBoundFiles(t *testing.T) {
	root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
	linked := filepath.Join(root, "source.md")
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "source.md")
	if err := os.WriteFile(external, []byte("coverage source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, linked); err != nil {
		t.Fatal(err)
	}
	before := preflightStateBytes(t, root)
	projection, err := PreflightGoalPlan(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "source-unavailable" {
		t.Fatalf("diagnostics = %#v", projection.Diagnostics)
	}
	if after := preflightStateBytes(t, root); !bytes.Equal(before, after) {
		t.Fatal("preflight mutated state")
	}
}

func TestPreflightGoalPlanRejectsIncompleteMapping(t *testing.T) {
	root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
	request.NodeMappings = request.NodeMappings[:1]
	projection, err := PreflightGoalPlan(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "mapping-mismatch" {
		t.Fatalf("diagnostics = %#v", projection.Diagnostics)
	}
}

func TestPreflightGoalPlanRejectsInvalidUTF8Manifest(t *testing.T) {
	root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
	path := filepath.Join(root, request.ManifestPath)
	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	malformed := bytes.Replace(manifestBytes, []byte(`"id":"plan"`), []byte("\"id\":\"pl\xffan\""), 1)
	if bytes.Equal(malformed, manifestBytes) {
		t.Fatal("fixture manifest did not contain the expected plan id")
	}
	if err := os.WriteFile(path, malformed, 0644); err != nil {
		t.Fatal(err)
	}
	projection, err := PreflightGoalPlan(context.Background(), root, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "malformed-artifact" {
		t.Fatalf("diagnostics = %#v", projection.Diagnostics)
	}
}

func TestPreflightGoalPlanRejectsInvalidBindingsWithoutMutation(t *testing.T) {
	tests := []struct {
		name           string
		mutateManifest func(map[string]any)
		mutateReview   func(map[string]any)
		wantCode       string
	}{
		{
			name: "missing node",
			mutateManifest: func(manifest map[string]any) {
				manifest["nodes"] = manifest["nodes"].([]any)[:1]
			},
			wantCode: "invalid-topology",
		},
		{
			name: "null reviewed sources is not an array",
			mutateManifest: func(manifest map[string]any) {
				manifest["reviewedSources"] = nil
			},
			wantCode: "malformed-artifact",
		},
		{
			name: "duplicate node",
			mutateManifest: func(manifest map[string]any) {
				nodes := manifest["nodes"].([]any)
				manifest["nodes"] = append(nodes, nodes[0])
			},
			wantCode: "invalid-topology",
		},
		{
			name: "edge to missing node",
			mutateManifest: func(manifest map[string]any) {
				nodes := manifest["nodes"].([]any)
				second := nodes[1].(map[string]any)
				second["dependsOn"] = []any{"missing"}
			},
			wantCode: "invalid-topology",
		},
		{
			name: "cycle",
			mutateManifest: func(manifest map[string]any) {
				nodes := manifest["nodes"].([]any)
				first := nodes[0].(map[string]any)
				first["dependsOn"] = []any{"b"}
			},
			wantCode: "invalid-topology",
		},
		{
			name: "coverage approval drift",
			mutateReview: func(review map[string]any) {
				review["manifestSha256"] = "not-the-supplied-manifest-digest"
			},
			wantCode: "approval-binding-mismatch",
		},
		{
			name: "coverage index drift",
			mutateReview: func(review map[string]any) {
				review["coverageIndex"].(map[string]any)["batchId"] = "another-batch"
			},
			wantCode: "approval-binding-mismatch",
		},
		{
			name: "unsupported review schema",
			mutateReview: func(review map[string]any) {
				review["schemaVersion"] = "2.0.0"
			},
			wantCode: "unsupported-schema",
		},
		{
			name: "null review sources is not an array",
			mutateReview: func(review map[string]any) {
				review["reviewedSources"] = nil
			},
			wantCode: "approval-binding-mismatch",
		},
		{
			name: "declaration digest drift",
			mutateManifest: func(manifest map[string]any) {
				manifest["declaration"].(map[string]any)["sha256"] = strings.Repeat("0", 64)
			},
			wantCode: "digest-mismatch",
		},
		{
			name: "readiness digest drift",
			mutateManifest: func(manifest map[string]any) {
				manifest["nodes"].([]any)[0].(map[string]any)["readinessContract"].(map[string]any)["sha256"] = strings.Repeat("0", 64)
			},
			wantCode: "digest-mismatch",
		},
		{
			name: "unsupported manifest schema",
			mutateManifest: func(manifest map[string]any) {
				manifest["schemaVersion"] = "2.0.0"
			},
			wantCode: "unsupported-schema",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
			mutatePreflightArtifacts(t, root, test.mutateManifest, test.mutateReview)
			before := preflightStateBytes(t, root)
			projection, err := PreflightGoalPlan(context.Background(), root, request)
			if err != nil {
				t.Fatal(err)
			}
			if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != test.wantCode {
				t.Fatalf("diagnostics = %#v", projection.Diagnostics)
			}
			if after := preflightStateBytes(t, root); !bytes.Equal(before, after) {
				t.Fatal("preflight mutated state")
			}
		})
	}
}

func TestPreflightGoalPlanFileRejectsMalformedOrEscapingRequests(t *testing.T) {
	root, request := preflightFixture(t, [][2]string{{"a", "b"}}, false)
	valid, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	goalField := []byte(`"goalId":"goal"`)
	invalidUTF8 := bytes.Replace(valid, goalField, []byte("\"goalId\":\"go\xffal\""), 1)
	duplicateKey := bytes.Replace(valid, goalField, []byte(`"goalId":"goal","goalId":"goal"`), 1)
	unknownField := bytes.Replace(valid, goalField, []byte(`"goalId":"goal","unexpected":true`), 1)
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "invalid UTF-8", body: invalidUTF8},
		{name: "duplicate key", body: duplicateKey},
		{name: "unknown field", body: unknownField},
		{name: "unbound source list", body: bytes.Replace(valid, []byte(`"nodeMappings"`), []byte(`"sources":[],"nodeMappings"`), 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if bytes.Equal(test.body, valid) {
				t.Fatal("malformed request fixture was not changed")
			}
			if err := os.WriteFile(filepath.Join(root, "request.json"), test.body, 0644); err != nil {
				t.Fatal(err)
			}
			before := preflightStateBytes(t, root)
			projection, err := PreflightGoalPlanFile(context.Background(), root, "request.json")
			if err != nil {
				t.Fatal(err)
			}
			if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "invalid-request" {
				t.Fatalf("diagnostics = %#v", projection.Diagnostics)
			}
			if after := preflightStateBytes(t, root); !bytes.Equal(before, after) {
				t.Fatal("preflight mutated state")
			}
		})
	}

	externalRoot := t.TempDir()
	externalRequest := filepath.Join(externalRoot, "request.json")
	if err := os.WriteFile(externalRequest, valid, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalRequest, filepath.Join(root, "external-request.json")); err != nil {
		t.Fatal(err)
	}
	projection, err := PreflightGoalPlanFile(context.Background(), root, "external-request.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "invalid-request" {
		t.Fatalf("external request diagnostics = %#v", projection.Diagnostics)
	}
}

func preflightFixture(t *testing.T, edges [][2]string, drift bool) (string, GoalPreflightRequest) {
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
	nodes := []string{"a", "b", "c", "d"}
	used := map[string]bool{}
	for _, edge := range edges {
		used[edge[0]], used[edge[1]] = true, true
	}
	var refs []string
	for _, ref := range nodes {
		if used[ref] {
			refs = append(refs, ref)
		}
	}
	now := time.Unix(0, 0).UTC()
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoal("goal", "Goal", "", repositoryRoot, now); err != nil {
			return err
		}
		ids := map[string]string{}
		for _, ref := range refs {
			deps := []string{}
			for _, edge := range edges {
				if edge[1] == ref {
					deps = append(deps, ids[edge[0]])
				}
			}
			item, err := state.AddWork("goal", "story-"+ref, deps, now)
			if err != nil {
				return err
			}
			ids[ref] = item.ID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := []byte("coverage source")
	if err := os.WriteFile(filepath.Join(root, "source.md"), source, 0644); err != nil {
		t.Fatal(err)
	}
	coverageIndex := map[string]any{"batchId": "BR-001-goal-plan-fixture", "fingerprint": testDigest([]byte("coverage-index"))}
	reviewedSources := []any{map[string]any{"path": "source.md", "sha256": testDigest(source)}}
	manifestNodes := make([]any, 0, len(refs))
	planNodes := make([]any, 0, len(refs))
	for _, ref := range refs {
		storyRef := "story-" + ref
		readinessPath := storyRef + "/readiness.json"
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, readinessPath)), 0755); err != nil {
			t.Fatal(err)
		}
		readiness := []byte("{\"format\":\"opaque\",\"node\":\"" + ref + "\"}")
		if err := os.WriteFile(filepath.Join(root, readinessPath), readiness, 0644); err != nil {
			t.Fatal(err)
		}
		dependsOn := []string{}
		for _, edge := range edges {
			if edge[1] == ref {
				dependsOn = append(dependsOn, edge[0])
			}
		}
		sort.Strings(dependsOn)
		planNodes = append(planNodes, map[string]any{"nodeRef": ref, "storyRef": storyRef, "dependsOn": dependsOn})
		manifestNodes = append(manifestNodes, map[string]any{
			"nodeRef": ref, "storyRef": storyRef,
			"readinessContract": map[string]any{"path": readinessPath, "sha256": testDigest(readiness)},
			"dependsOn":         dependsOn,
		})
	}
	declaration := map[string]any{
		"schemaVersion": "1.0.0", "plan": map[string]any{"id": "plan", "revision": 1}, "nodes": planNodes,
	}
	declarationBytes, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "declaration.json"), declarationBytes, 0644); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schemaVersion": "1.0.0",
		"plan":          map[string]any{"id": "plan", "revision": 1},
		"declaration":   map[string]any{"path": "declaration.json", "sha256": testDigest(declarationBytes)},
		"nodes":         manifestNodes, "reviewedSources": reviewedSources, "coverageIndex": coverageIndex,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifestBytes, 0644); err != nil {
		t.Fatal(err)
	}
	review := map[string]any{
		"schemaVersion": "1.0.0", "reviewId": "00000000-0000-4000-8000-000000000001",
		"manifestSha256": testDigest(manifestBytes), "reviewedSources": reviewedSources,
		"coverageIndex": coverageIndex, "conclusion": "approved",
		"reviewer":   map[string]any{"name": "reviewer", "assurance": "self-asserted"},
		"reviewedAt": "2025-01-02T03:04:05.000Z",
	}
	reviewBytes, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "review.json"), reviewBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if drift {
		if err := os.WriteFile(filepath.Join(root, "source.md"), []byte("drift"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mappings := make([]GoalPlanNodeMapping, 0, len(refs))
	for i, ref := range refs {
		mappings = append(mappings, GoalPlanNodeMapping{PlanNodeRef: ref, WorkItemID: "WI-" + []string{"001", "002", "003", "004"}[i]})
	}
	return root, GoalPreflightRequest{FormatVersion: goalPreflightRequestVersion, GoalID: "goal", ManifestPath: "manifest.json", CoverageReviewPath: "review.json", NodeMappings: mappings}
}

func testDigest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

func mutatePreflightArtifacts(t *testing.T, root string, mutateManifest func(map[string]any), mutateReview func(map[string]any)) {
	t.Helper()
	manifestPath := filepath.Join(root, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if mutateManifest != nil {
		var manifest map[string]any
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			t.Fatal(err)
		}
		mutateManifest(manifest)
		manifestBytes, err = json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil {
			t.Fatal(err)
		}
	}
	reviewPath := filepath.Join(root, "review.json")
	reviewBytes, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	var review map[string]any
	if err := json.Unmarshal(reviewBytes, &review); err != nil {
		t.Fatal(err)
	}
	if mutateManifest != nil {
		review["manifestSha256"] = testDigest(manifestBytes)
	}
	if mutateReview != nil {
		mutateReview(review)
	}
	reviewBytes, err = json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewPath, reviewBytes, 0644); err != nil {
		t.Fatal(err)
	}
}

func preflightStateBytes(t *testing.T, root string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return body
}
