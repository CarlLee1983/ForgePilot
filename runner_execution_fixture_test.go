package forgepilot_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const runnerTestGenerationCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const runnerTestPayloadDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// managedRunnerTestImage gives the CLI under test the same process-image shape
// that Bootstrap installs, inside a disposable HOME. The helper implements only
// the generation and retention protocol needed by these Runner tests.
func managedRunnerTestImage(t *testing.T, builtBinary string) (string, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".local", "share", "forgepilot")
	version := filepath.Join(root, "versions", runnerTestGenerationCommit)
	binary := filepath.Join(version, "bin", "forgepilot")
	helper := filepath.Join(version, "libexec", "forgepilot-bootstrap")
	for _, directory := range []string{filepath.Dir(binary), filepath.Dir(helper), filepath.Join(home, ".local", "bin")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(builtBinary, binary); err != nil {
		t.Fatal(err)
	}
	current, err := json.Marshal(map[string]any{
		"protocol_version": 1, "generation_id": runnerTestGenerationCommit,
		"payload_digest": runnerTestPayloadDigest, "forgepilot_path": binary, "helper_path": helper,
	})
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$1:$2" in
  generation-v1:current)
    printf '%%s\n' %s ;;
  retention-v1:acquire|retention-v1:release)
    [ "$3" = --generation ] && [ "$4" = %s ] && [ "$5" = --payload-digest ] && [ "$6" = %s ] && [ "$7" = --reference ] && [ -n "$8" ] || exit 9
    if [ "$2" = acquire ]; then result=acquired; else result=released; fi
    printf '{"protocol_version":1,"result":"%%s","generation_id":"%%s","payload_digest":"%%s"}\n' "$result" "$4" "$6" ;;
  *) exit 9 ;;
esac
`, shellQuote(string(current)), shellQuote(runnerTestGenerationCommit), shellQuote(runnerTestPayloadDigest))
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("versions/"+runnerTestGenerationCommit, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "current", "libexec", "forgepilot-bootstrap"),
		filepath.Join(home, ".local", "bin", "forgepilot-bootstrap")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	worker := filepath.Join(t.TempDir(), "codex-test-worker")
	workerScript := `#!/bin/sh
if [ "$1" = --version ]; then
  echo 'codex-test-worker 1.0'
  exit 0
fi
[ "$1" = exec ] && [ -n "$FORGEPILOT_FAKE_AGENT" ] || exit 9
exec "$FORGEPILOT_FAKE_AGENT" "$@"
`
	if err := os.WriteFile(worker, []byte(workerScript), 0700); err != nil {
		t.Fatal(err)
	}
	worker, err = filepath.EvalSymlinks(worker)
	if err != nil {
		t.Fatal(err)
	}
	return binary, worker
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func (fixture runnerFixture) realCodexWorker(t *testing.T, home, codexPath string) string {
	t.Helper()
	worker := filepath.Join(t.TempDir(), "codex-smoke-worker")
	script := "#!/bin/sh\nHOME=" + shellQuote(home) + " exec " + shellQuote(codexPath) + " \"$@\"\n"
	if err := os.WriteFile(worker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	worker, err := filepath.EvalSymlinks(worker)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func (fixture runnerFixture) authorizeGoal(t *testing.T, goalID string) {
	t.Helper()
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	planDir := filepath.ToSlash(filepath.Join("specs", "plans", goalID))
	sourcePath := planDir + "/source.md"
	source := []byte("runner fixture coverage source\n")
	write(t, filepath.Join(fixture.root, sourcePath), string(source))
	sources := []any{map[string]any{"path": sourcePath, "sha256": runnerPlanDigest(source)}}
	coverageIndex := map[string]any{"batchId": "BR-001-runner-fixture", "fingerprint": runnerPlanDigest([]byte(goalID))}
	identity := map[string]any{"id": "runner-" + goalID, "revision": 1}
	var declarationNodes, manifestNodes, mappings []any
	for _, item := range state.WorkItems {
		if item.GoalID != goalID {
			continue
		}
		readinessPath := item.StoryRef + "/readiness.json"
		readiness, err := os.ReadFile(filepath.Join(fixture.root, filepath.FromSlash(readinessPath)))
		if err != nil {
			t.Fatal(err)
		}
		dependencies := append([]string{}, item.DependsOn...)
		declarationNodes = append(declarationNodes, map[string]any{
			"nodeRef": item.ID, "storyRef": item.StoryRef, "dependsOn": dependencies,
		})
		manifestNodes = append(manifestNodes, map[string]any{
			"nodeRef": item.ID, "storyRef": item.StoryRef, "dependsOn": dependencies,
			"readinessContract": map[string]any{"path": readinessPath, "sha256": runnerPlanDigest(readiness)},
		})
		mappings = append(mappings, map[string]any{"planNodeRef": item.ID, "workItemId": item.ID})
	}
	declarationPath := planDir + "/declaration.json"
	declaration := runnerPlanJSON(t, map[string]any{"schemaVersion": "1.0.0", "plan": identity, "nodes": declarationNodes})
	write(t, filepath.Join(fixture.root, filepath.FromSlash(declarationPath)), string(declaration))
	manifestPath := planDir + "/manifest.json"
	manifest := runnerPlanJSON(t, map[string]any{
		"schemaVersion": "1.0.0", "plan": identity,
		"declaration": map[string]any{"path": declarationPath, "sha256": runnerPlanDigest(declaration)},
		"nodes":       manifestNodes, "reviewedSources": sources, "coverageIndex": coverageIndex,
	})
	write(t, filepath.Join(fixture.root, filepath.FromSlash(manifestPath)), string(manifest))
	reviewPath := planDir + "/review.json"
	review := runnerPlanJSON(t, map[string]any{
		"schemaVersion": "1.0.0", "reviewId": "00000000-0000-4000-8000-000000000001",
		"manifestSha256": runnerPlanDigest(manifest), "reviewedSources": sources, "coverageIndex": coverageIndex,
		"conclusion": "approved", "reviewer": map[string]any{"name": "runner fixture", "assurance": "self-asserted"},
		"reviewedAt": "2025-01-02T03:04:05.000Z",
	})
	write(t, filepath.Join(fixture.root, filepath.FromSlash(reviewPath)), string(review))
	requestPath := planDir + "/execution-request.json"
	request := runnerPlanJSON(t, map[string]any{
		"formatVersion": "forgepilot.execution-plan-request/v2",
		"goalPlanRequest": map[string]any{
			"formatVersion": "forgepilot.goal-preflight-request/v1", "goalId": goalID,
			"manifestPath": manifestPath, "coverageReviewPath": reviewPath, "nodeMappings": mappings,
		},
		"workerProfile": map[string]any{
			"runtime": "codex", "executablePath": fixture.worker, "model": fixture.model,
			"effort": "medium", "sandbox": "workspace-write",
		},
		"engineGeneration": map[string]any{"sourceCommit": runnerTestGenerationCommit, "payloadSHA256": runnerTestPayloadDigest},
		"caps": map[string]any{
			"maxSteps": 500, "maxTechnicalAttemptsPerNode": 50, "maxRuns": fixture.maxRuns, "maxRecoveries": 100,
			"maxHandoffBytes": 1 << 20, "maxWriteBytes": fixture.maxWriteBytes,
			"maxRunBytes": fixture.maxRunBytes, "maxTotalBytes": fixture.maxTotalBytes,
		},
		"expiresAt": time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339),
	})
	write(t, filepath.Join(fixture.root, filepath.FromSlash(requestPath)), string(request))
	commitAll(t, fixture.root, "runner execution plan")
	output, err := command(fixture.binary, fixture.root, "execution", "plan", "--request", requestPath, "--json")
	if err != nil {
		t.Fatalf("execution plan: %v\n%s", err, output)
	}
	var projection struct {
		ApprovalToken string `json:"approvalToken"`
		Diagnostics   []any  `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(output), &projection); err != nil || projection.ApprovalToken == "" || len(projection.Diagnostics) != 0 {
		t.Fatalf("execution plan projection: %v\n%s", err, output)
	}
	mustRun(t, fixture.binary, fixture.root, "execution", "authorize", "--request", requestPath,
		"--approval-token", projection.ApprovalToken, "--by", "runner-fixture", "--json")
}

func (fixture runnerFixture) allowAnotherRun(t *testing.T, goalID string) {
	t.Helper()
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		t.Fatalf("goal %q has no execution authorization", goalID)
	}
	initialRequestPath := filepath.ToSlash(filepath.Join("specs", "plans", goalID, "execution-request.json"))
	data, err := os.ReadFile(filepath.Join(fixture.root, filepath.FromSlash(initialRequestPath)))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	request["expectedAuthorizationDigest"] = goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].Digest
	caps := request["caps"].(map[string]any)
	caps["maxRuns"] = int(caps["maxRuns"].(float64)) + 1
	requestPath := filepath.ToSlash(filepath.Join(".forgepilot", "fixture-execution-revision.json"))
	write(t, filepath.Join(fixture.root, filepath.FromSlash(requestPath)), string(runnerPlanJSON(t, request)))
	output, err := command(fixture.binary, fixture.root, "execution", "revise", "plan", "--request", requestPath, "--json")
	if err != nil {
		t.Fatalf("execution revise plan: %v\n%s", err, output)
	}
	var projection struct {
		ApprovalToken string `json:"approvalToken"`
		Diagnostics   []any  `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(output), &projection); err != nil || projection.ApprovalToken == "" || len(projection.Diagnostics) != 0 {
		t.Fatalf("execution revise plan projection: %v\n%s", err, output)
	}
	mustRun(t, fixture.binary, fixture.root, "execution", "revise", "authorize", "--request", requestPath,
		"--approval-token", projection.ApprovalToken, "--by", "runner-fixture", "--json")
}

func runnerPlanJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runnerPlanDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type runnerFixtureGenerationResolver struct{ helper string }

func (resolver runnerFixtureGenerationResolver) Resolve(context.Context) (app.ResolvedBootstrapGeneration, error) {
	return app.ResolvedBootstrapGeneration{Generation: work.ExecutionEngineGeneration{
		SourceCommit: runnerTestGenerationCommit, PayloadSHA256: runnerTestPayloadDigest,
	}, HelperPath: resolver.helper}, nil
}

func (fixture runnerFixture) generationResolver() app.EngineGenerationResolver {
	version := filepath.Dir(filepath.Dir(fixture.binary))
	return runnerFixtureGenerationResolver{helper: filepath.Join(version, "libexec", "forgepilot-bootstrap")}
}
