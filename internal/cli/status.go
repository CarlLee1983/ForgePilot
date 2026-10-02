package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const statusUsage = "usage: forgepilot status [--goal <goal-id>] [--work <work-id>] [--json]"

type statusOptions struct {
	goalID, workID string
	asJSON         bool
}

func parseStatusArgs(args []string) (statusOptions, error) {
	var options statusOptions
	seen := map[string]bool{}
	for len(args) > 0 {
		name := args[0]
		if name != "--goal" && name != "--work" && name != "--json" || seen[name] {
			return statusOptions{}, errors.New(statusUsage)
		}
		seen[name] = true
		if name == "--json" {
			options.asJSON = true
			args = args[1:]
			continue
		}
		if len(args) < 2 || args[1] == "" || strings.HasPrefix(args[1], "--") {
			return statusOptions{}, errors.New(statusUsage)
		}
		if name == "--goal" {
			options.goalID = args[1]
		} else {
			options.workID = args[1]
		}
		args = args[2:]
	}
	return options, nil
}

// statusJSON is the machine-readable shape of `status --json`: the selected
// Goals with their Work Items, and the one action `next` would recommend.
type statusJSON struct {
	Goals []goalJSON `json:"goals"`
	Next  nextJSON   `json:"next"`
}

type goalJSON struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	Reason          string     `json:"reason"`
	RequireApproval bool       `json:"require_approval"`
	WorkItems       []workJSON `json:"work_items"`
}

type workJSON struct {
	ID           string        `json:"id"`
	StoryRef     string        `json:"story_ref"`
	Status       string        `json:"status"`
	DependsOn    []string      `json:"depends_on"`
	Completion   string        `json:"completion"`
	Verification *evidenceJSON `json:"verification"`
	Review       *evidenceJSON `json:"review"`
	// VerificationStale is null when the repository facts needed to judge it
	// could not be read; StaleUnknownReason then says why ("" otherwise).
	VerificationStale  *bool          `json:"verification_stale"`
	StaleUnknownReason string         `json:"stale_unknown_reason"`
	OpenGates          []gateJSON     `json:"open_gates"`
	CannotAdvance      []obstacleJSON `json:"cannot_advance"`
}

type evidenceJSON struct {
	ID            string `json:"id"`
	Result        string `json:"result"`
	Revision      string `json:"revision"`
	CandidateKind string `json:"candidate_kind"`
}

type gateJSON struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

type obstacleJSON struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Message string `json:"message"`
}

// status is a pure query. A repository without the facts needed to judge
// staleness is not an error, but it is not "fresh" either: what is known is
// reported and the unjudgeable Evidence is marked "staleness unknown".
func status(args []string, root string, output io.Writer) error {
	options, err := parseStatusArgs(args)
	if err != nil {
		return err
	}
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	if options.goalID != "" {
		if _, ok := state.GoalByID(options.goalID); !ok {
			return fmt.Errorf("unknown goal %q", options.goalID)
		}
	}
	if options.workID != "" {
		goal, ok := state.GoalOfWorkItem(options.workID)
		if !ok {
			return fmt.Errorf("unknown work item %q", options.workID)
		}
		if options.goalID != "" && goal.ID != options.goalID {
			return fmt.Errorf("work item %q belongs to goal %q, not %q", options.workID, goal.ID, options.goalID)
		}
	}
	facts := gatherStatusFacts(&state, root)
	scopeGoal := options.goalID
	if scopeGoal == "" && options.workID != "" {
		// --work narrows to the Goal the work belongs to.
		goal, _ := state.GoalOfWorkItem(options.workID)
		scopeGoal = goal.ID
	}
	var goals []work.Goal
	for _, goal := range state.Goals {
		if scopeGoal == "" || goal.ID == scopeGoal {
			goals = append(goals, goal)
		}
	}
	if options.asJSON {
		return writeJSON(output, newStatusJSON(&state, goals, options, facts))
	}
	if options.workID != "" {
		summary, err := state.WorkSummary(options.workID, facts.RepositoryState)
		if err != nil {
			return err
		}
		return writeWorkSummary(output, &state, summary, facts)
	}
	return writeFullStatus(output, &state, goals, facts)
}

// statusFacts are the repository facts a status read could gather, with the
// reason for each one it could not. A fact that cannot be read is reported as
// unknown, never silently treated as "not stale".
type statusFacts struct {
	work.RepositoryState
	headErr, snapshotErr error
}

// gatherStatusFacts reads HEAD, and the workspace snapshot only when some
// unfinished work has SNAPSHOT Evidence to compare.
func gatherStatusFacts(state *work.State, root string) statusFacts {
	facts := statusFacts{}
	facts.Revision, facts.headErr = repository.Head(context.Background(), root)
	for _, item := range state.WorkItems {
		latest, ok := state.LatestVerification(item.ID)
		if item.Status != work.Done && ok && latest.CandidateKind == work.SnapshotCandidate {
			workspace, err := repository.InspectSnapshot(context.Background(), root)
			if err != nil {
				facts.snapshotErr = err
			} else {
				facts.Revision, facts.SnapshotDigest = workspace.BaseRevision, workspace.Digest
			}
			break
		}
	}
	for _, item := range state.WorkItems {
		// A Verification Run whose process is gone is reported, not repaired:
		// status is a pure query, and reclaiming it would be a write.
		if item.Status == work.Verifying && !storage.VerificationRunning(root, item.ID) {
			if facts.AbandonedRuns == nil {
				facts.AbandonedRuns = map[string]bool{}
			}
			facts.AbandonedRuns[item.ID] = true
		}
	}
	return facts
}

// unknownStaleness explains why an unfinished Work Item's Evidence cannot be
// judged fresh or stale, or returns "" when it can (or there is no Evidence).
func (f statusFacts) unknownStaleness(state *work.State, id string) string {
	if state.WorkItemStatus(id) == work.Done {
		return ""
	}
	latest, ok := state.LatestVerification(id)
	if !ok {
		return ""
	}
	if latest.CandidateKind == work.SnapshotCandidate {
		if f.snapshotErr != nil {
			return "cannot read the workspace snapshot: " + f.snapshotErr.Error()
		}
		return ""
	}
	if f.headErr != nil {
		return "cannot read HEAD: " + f.headErr.Error()
	}
	return ""
}

// undecidableNext explains why the next action cannot be decided: some REVIEW
// work's staleness is unknown. It is the same condition under which `next`
// fails on its own, so the two entry points agree.
func (f statusFacts) undecidableNext(state *work.State) string {
	for _, item := range state.WorkItems {
		if item.Status != work.Review {
			continue
		}
		if reason := f.unknownStaleness(state, item.ID); reason != "" {
			return fmt.Sprintf("cannot decide the next action: staleness of %s is unknown (%s)", item.ID, reason)
		}
	}
	return ""
}

func writeFullStatus(output io.Writer, state *work.State, goals []work.Goal, facts statusFacts) error {
	for _, goal := range goals {
		heading := fmt.Sprintf("Goal %s %s: %s", goal.ID, goal.Status, goal.Title)
		if goal.Reason != "" {
			heading += fmt.Sprintf(" (%s)", goal.Reason)
		}
		if _, err := fmt.Fprintln(output, heading); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "  Approval required: %s\n", yesNo(goal.RequireApproval)); err != nil {
			return err
		}
		for _, item := range state.WorkItems {
			if item.GoalID != goal.ID {
				continue
			}
			note := ""
			if item.Status == work.Verifying && facts.AbandonedRuns[item.ID] {
				// verify reclaims an abandoned run before anything can refuse the
				// command, so this instruction works even when the work is blocked.
				note = " (verifier is gone; run forgepilot verify to recover)"
			}
			evidence := verificationSummary(state, item.ID, facts.Revision, facts.SnapshotDigest)
			if reason := facts.unknownStaleness(state, item.ID); reason != "" {
				evidence += " (staleness unknown: " + reason + ")"
			}
			if _, err := fmt.Fprintf(output, "  %s %s %s%s\n    %s\n", item.ID, state.DisplayStatus(item.ID), item.StoryRef, note, evidence); err != nil {
				return err
			}
			if goal.RequireApproval {
				if _, err := fmt.Fprintf(output, "    %s\n", reviewSummary(state, item.ID)); err != nil {
					return err
				}
			}
			for _, line := range gateSummary(state, item.ID) {
				if _, err := fmt.Fprintf(output, "    %s\n", line); err != nil {
					return err
				}
			}
			for _, obstacle := range state.Obstacles(item.ID) {
				if _, err := fmt.Fprintf(output, "    Cannot advance: %s\n", obstacle.Message); err != nil {
					return err
				}
			}
		}
	}
	if reason := facts.undecidableNext(state); reason != "" {
		_, err := fmt.Fprintf(output, "Next: unknown (%s)\n", reason)
		return err
	}
	action := state.ActionableNext(facts.RepositoryState)
	switch {
	case action.Item.ID != "":
		_, err := fmt.Fprintf(output, "Next: %s\n", action.Item.ID)
		return err
	case action.Kind == work.NextActionWait:
		// Waiting is not "nothing": list what is waited on, as `next` does.
		return writeNextText(output, state, action)
	}
	_, err := fmt.Fprintln(output, "Next: none")
	return err
}

func writeWorkSummary(output io.Writer, state *work.State, summary work.WorkItemSummary, facts statusFacts) error {
	blocking := "none"
	if len(summary.BlockingGates) > 0 {
		ids := make([]string, 0, len(summary.BlockingGates))
		for _, gate := range summary.BlockingGates {
			ids = append(ids, gate.ID)
		}
		blocking = strings.Join(ids, ", ")
	}
	if _, err := fmt.Fprintf(output, "%s %s\nGoal: %s %s\nApproval required: %s\nStory: %s\nVerification: %s\nReview: %s\nBlocking gates: %s\nCompletion: %s\n",
		summary.Item.ID, state.DisplayStatus(summary.Item.ID),
		summary.Goal.ID, summary.Goal.Status,
		yesNo(summary.Goal.RequireApproval),
		summary.Item.StoryRef,
		workVerificationSummary(summary, facts.unknownStaleness(state, summary.Item.ID)),
		workReviewSummary(summary),
		blocking,
		summary.Completion); err != nil {
		return err
	}
	for _, obstacle := range state.Obstacles(summary.Item.ID) {
		if _, err := fmt.Fprintf(output, "Cannot advance: %s\n", obstacle.Message); err != nil {
			return err
		}
	}
	return nil
}

func workVerificationSummary(summary work.WorkItemSummary, unknownReason string) string {
	if !summary.HasVerification {
		return "not run"
	}
	result := fmt.Sprintf("%s %s at %s", summary.Verification.ID, summary.Verification.Result, shortRevision(summary.Verification.Revision))
	if summary.Verification.CandidateKind == work.SnapshotCandidate {
		result += " (snapshot)"
	}
	if summary.VerificationStale {
		result += " (stale)"
	}
	if unknownReason != "" {
		result += " (staleness unknown: " + unknownReason + ")"
	}
	return result
}

func workReviewSummary(summary work.WorkItemSummary) string {
	if !summary.Goal.RequireApproval {
		return "not required"
	}
	if !summary.HasReview {
		return "not reviewed"
	}
	return fmt.Sprintf("%s %s at %s", summary.Review.ID, summary.Review.Result, shortRevision(summary.Review.Revision))
}

func newStatusJSON(state *work.State, goals []work.Goal, options statusOptions, facts statusFacts) statusJSON {
	result := statusJSON{Goals: []goalJSON{}}
	if reason := facts.undecidableNext(state); reason != "" {
		result.Next = nextJSON{Action: "UNKNOWN", Reason: reason, Waiting: []waitingJSON{}}
	} else {
		result.Next = newNextJSON(state, state.ActionableNext(facts.RepositoryState))
	}
	for _, goal := range goals {
		entry := goalJSON{ID: goal.ID, Title: goal.Title, Status: string(goal.Status), Reason: goal.Reason,
			RequireApproval: goal.RequireApproval, WorkItems: []workJSON{}}
		for _, item := range state.WorkItems {
			if item.GoalID != goal.ID || options.workID != "" && item.ID != options.workID {
				continue
			}
			summary, err := state.WorkSummary(item.ID, facts.RepositoryState)
			if err != nil {
				continue
			}
			entry.WorkItems = append(entry.WorkItems, newWorkJSON(state, summary, facts.unknownStaleness(state, item.ID)))
		}
		result.Goals = append(result.Goals, entry)
	}
	return result
}

func newWorkJSON(state *work.State, summary work.WorkItemSummary, unknownReason string) workJSON {
	item := summary.Item
	entry := workJSON{ID: item.ID, StoryRef: item.StoryRef, Status: state.DisplayStatus(item.ID),
		DependsOn: append([]string{}, item.DependsOn...), Completion: string(summary.Completion),
		OpenGates: []gateJSON{}, CannotAdvance: []obstacleJSON{}}
	if unknownReason != "" {
		entry.StaleUnknownReason = unknownReason
	} else {
		stale := summary.VerificationStale
		entry.VerificationStale = &stale
	}
	if summary.HasVerification {
		entry.Verification = &evidenceJSON{ID: summary.Verification.ID, Result: string(summary.Verification.Result),
			Revision: summary.Verification.Revision, CandidateKind: string(summary.Verification.CandidateKind)}
	}
	if summary.HasReview {
		entry.Review = &evidenceJSON{ID: summary.Review.ID, Result: string(summary.Review.Result),
			Revision: summary.Review.Revision, CandidateKind: string(summary.Review.CandidateKind)}
	}
	for _, gate := range summary.BlockingGates {
		entry.OpenGates = append(entry.OpenGates, gateJSON{ID: gate.ID, Question: gate.Question})
	}
	for _, obstacle := range state.Obstacles(item.ID) {
		entry.CannotAdvance = append(entry.CannotAdvance, obstacleJSON{Kind: string(obstacle.Kind), Ref: obstacle.Ref, Message: obstacle.Message})
	}
	return entry
}
