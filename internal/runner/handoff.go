package runner

import (
	"io"
	"os"
	"strings"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// handoff assembles one session's briefing from durable facts. Everything in it
// is either read from state or named as a path: no environment is copied, no
// token is passed, and nothing a previous session wrote is trusted beyond being
// quoted as that session's claim.
func (runner *Runner) handoff(action work.NextAction, decision app.Decision, attempt int) (string, error) {
	state, err := storage.Load(runner.options.Root)
	if err != nil {
		return "", err
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
		briefing.FailureExcerpt, briefing.FailureLogPath = runner.lastFailure(&state, item.ID)
	}
	return briefing.Render(runner.options.Budget.MaxHandoffBytes), nil
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
func (runner *Runner) lastFailure(state *work.State, itemID string) (string, string) {
	verification, ok := state.LatestVerification(itemID)
	if !ok || verification.Result != work.Fail {
		return "", ""
	}
	path := runner.findLog(itemID, verification.Revision)
	if path == "" {
		return "", ""
	}
	limit := runner.options.Excerpt
	if limit <= 0 {
		limit = DefaultExcerpt
	}
	return tail(path, limit), path
}

// findLog locates the log a run against this revision wrote. Logs are keyed by
// run rather than by Evidence (ADR-0012), so the match is by Work Item and
// revision prefix, which is exactly how a person would find it by hand.
func (runner *Runner) findLog(itemID, revision string) string {
	directory := runner.record.Workspace + string(os.PathSeparator) + ".forgepilot" + string(os.PathSeparator) + "logs"
	entries, err := os.ReadDir(directory)
	if err != nil {
		return ""
	}
	prefix := itemID + "-" + shortRevision(revision)
	latest := ""
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) && entry.Name() > latest {
			latest = entry.Name()
		}
	}
	if latest == "" {
		return ""
	}
	return directory + string(os.PathSeparator) + latest
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
	if size > int64(limit) {
		if _, err := file.Seek(size-int64(limit), io.SeekStart); err != nil {
			return ""
		}
		truncated = true
	}
	contents, err := io.ReadAll(io.LimitReader(file, int64(limit)))
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
