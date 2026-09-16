package runner

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// handoff assembles one session's briefing from durable facts. Everything in it
// is either read from state or named as a path: no environment is copied, no
// token is passed, and nothing a previous session wrote is trusted beyond being
// quoted as that session's claim.
func (runner *Runner) handoff(action work.NextAction, decision app.Decision, attempt int) (string, bool, error) {
	state, err := storage.Load(runner.options.Root)
	if err != nil {
		return "", false, err
	}
	current := state.ActionableNextForGoal(runner.record.GoalID, decision.Repository)
	if current.Kind != action.Kind || current.Item.ID != action.Item.ID {
		return "", false, nil
	}
	item := action.Item
	briefing := agent.Handoff{
		GoalID:       runner.record.GoalID,
		GoalTitle:    runner.record.GoalTitle,
		WorkItemID:   item.ID,
		StoryRef:     item.StoryRef,
		Action:       string(action.Kind),
		ActionReason: action.Reason,
		Candidate:    candidateDescription(decision),
		ProjectDocs:  runner.projectDocs(),
	}
	for _, gate := range resolvedGatesForHandoff(&state, item) {
		briefing.ResolvedDecisions = append(briefing.ResolvedDecisions, agent.ResolvedDecision{
			GateID: gate.ID, WorkItemID: gate.WorkItemID, Question: gate.Question,
			Choice: gate.Choice, Note: gate.Note,
		})
	}
	for _, dependencyID := range item.DependsOn {
		dependency := work.Item{}
		for _, candidate := range state.WorkItems {
			if candidate.ID == dependencyID {
				dependency = candidate
			}
		}
		evidence := ""
		if verification, ok := state.LatestVerification(dependencyID); ok {
			evidence = verification.ID + " " + string(verification.Result)
		}
		briefing.Dependencies = append(briefing.Dependencies, agent.Dependency{
			ID: dependencyID, Status: string(dependency.Status), StoryRef: dependency.StoryRef, Evidence: evidence})
	}
	for _, previous := range runner.record.AttemptsFor(item.ID) {
		briefing.Attempts = append(briefing.Attempts, agent.Attempt{
			Number: previous.Number, Outcome: previous.Outcome, Summary: previous.Summary})
	}
	if action.Kind == work.NextActionRepair {
		briefing.FailureExcerpt, briefing.FailureLogPath, err = runner.lastFailure(&state, item.ID)
		if err != nil {
			return "", false, err
		}
	}
	rendered, err := briefing.Render(runner.options.Budget.MaxHandoffBytes)
	return rendered, true, err
}

// resolvedGatesForHandoff projects authoritative decision context for one
// session. Relevance follows the Work Item DAG backwards through every
// prerequisite; Gate history itself stays in durable opening order.
func resolvedGatesForHandoff(state *work.State, item work.Item) []work.Gate {
	items := make(map[string]work.Item, len(state.WorkItems))
	for _, candidate := range state.WorkItems {
		items[candidate.ID] = candidate
	}
	relevant := map[string]bool{item.ID: true}
	pending := append([]string(nil), item.DependsOn...)
	for len(pending) > 0 {
		last := len(pending) - 1
		id := pending[last]
		pending = pending[:last]
		if relevant[id] {
			continue
		}
		relevant[id] = true
		if prerequisite, ok := items[id]; ok {
			pending = append(pending, prerequisite.DependsOn...)
		}
	}
	var gates []work.Gate
	for _, gate := range state.Gates {
		if relevant[gate.WorkItemID] && gate.Status == work.GateResolved {
			gates = append(gates, gate)
		}
	}
	return gates
}

func candidateDescription(decision app.Decision) string {
	facts := decision.Repository
	switch {
	case facts.SnapshotDigest != "":
		return "SNAPSHOT digest " + facts.SnapshotDigest + " on base " + facts.Revision
	case facts.Revision != "":
		return "COMMIT " + facts.Revision
	default:
		return ""
	}
}

// projectDocs names the repository's own instructions, if it has them. It lists
// paths rather than inlining contents: a briefing that pasted AGENTS.md would
// go stale the moment the file changed.
func (runner *Runner) projectDocs() []string {
	var docs []string
	for _, name := range []string{"AGENTS.md", "CONTEXT.md", "CLAUDE.md", "docs/architecture.md", "docs/adr/README.md"} {
		if _, err := os.Stat(runner.record.Workspace + string(os.PathSeparator) + name); err == nil {
			docs = append(docs, name)
		}
	}
	return docs
}

// lastFailure quotes the tail of the log the latest failing run wrote. The path
// is always given; the excerpt is bounded, because the cause of a failure is
// near the end and the whole log belongs in the file, not in the briefing.
func (runner *Runner) lastFailure(state *work.State, itemID string) (string, string, error) {
	verification, ok := state.LatestVerification(itemID)
	if !ok || verification.Result != work.Fail {
		return "", "", nil
	}
	path, err := runner.findLog(verification)
	if err != nil {
		return "", "", err
	}
	limit := runner.options.Excerpt
	if limit <= 0 {
		limit = DefaultExcerpt
	}
	return tail(path, limit), path, nil
}

// findLog locates the log for one settled Verification Run. New VR records use
// their durable execution identity; migrated LVR records retain the old
// Work-Item/revision lookup because no run-keyed filename existed for them.
func (runner *Runner) findLog(verification work.Evidence) (string, error) {
	if strings.HasPrefix(verification.VerificationRunID, "VR-") {
		path, err := repository.FindVerificationLog(runner.record.Workspace, verification.VerificationRunID)
		if err != nil {
			return "", fmt.Errorf("find log for verification run %s: %w", verification.VerificationRunID, err)
		}
		return path, nil
	}
	if !strings.HasPrefix(verification.VerificationRunID, "LVR-") {
		return "", fmt.Errorf("verification %s has unsupported run ID %q", verification.ID, verification.VerificationRunID)
	}
	directory := runner.record.Workspace + string(os.PathSeparator) + ".forgepilot" + string(os.PathSeparator) + "logs"
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", repository.ErrVerificationLogNotFound, verification.VerificationRunID)
		}
		return "", err
	}
	prefix := verification.WorkItemID + "-" + shortRevision(verification.Revision)
	match := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if match != "" {
			return "", fmt.Errorf("%w: %s", repository.ErrVerificationLogAmbiguous, verification.VerificationRunID)
		}
		match = entry.Name()
	}
	if match == "" {
		return "", fmt.Errorf("%w: %s", repository.ErrVerificationLogNotFound, verification.VerificationRunID)
	}
	return directory + string(os.PathSeparator) + match, nil
}

// tail reads the last limit bytes of a file. A canonical check's log is
// deliberately unbounded — ADR-0012 promises it holds that run's complete
// output — so a test suite that prints a great deal leaves a very large file,
// and reading it whole to quote its last few kilobytes would exhaust memory on
// precisely the path that runs after a failure.
func tail(path string, limit int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	truncated := false
	// One byte more than the limit is read on purpose: it is what tells Excerpt
	// the log did not fit, which is what makes it say so and drop the half line
	// the seek landed in the middle of.
	want := int64(limit)
	if size > int64(limit) {
		if _, err := file.Seek(size-int64(limit)-1, io.SeekStart); err != nil {
			return ""
		}
		want, truncated = int64(limit)+1, true
	}
	contents, err := io.ReadAll(io.LimitReader(file, want))
	if err != nil {
		return ""
	}
	text := string(contents)
	if !truncated {
		return strings.TrimSpace(text)
	}
	return agent.Excerpt(text, limit)
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
