package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func review(args []string, root string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot review <approve|reject>")
	}
	switch args[0] {
	case "approve":
		return recordReview(args[1:], root, output, work.Approved)
	case "reject":
		return recordReview(args[1:], root, output, work.Rejected)
	default:
		return fmt.Errorf("unknown review subcommand %q", args[0])
	}
}

func recordReview(args []string, root string, output io.Writer, result work.Result) error {
	// Name the command, not its outcome: "approved WI-001: ..." on a failure
	// reads as though the review had succeeded.
	verb := "review approve"
	if result == work.Rejected {
		verb = "review reject"
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		if result == work.Rejected {
			return errors.New("usage: forgepilot review reject <work-id> --reason <text> [--pr <owner/name#number>] [--by <identity>]")
		}
		return errors.New("usage: forgepilot review approve <work-id> [--pr <owner/name#number>] [--note <text>] [--by <identity>]")
	}
	id := args[0]
	allowed := map[string]bool{"by": false, "note": false, "pr": false}
	if result == work.Rejected {
		allowed = map[string]bool{"by": false, "reason": false, "pr": false}
	}
	values, err := flags(args[1:], allowed)
	if err != nil {
		return err
	}
	note := values.one("note")
	if result == work.Rejected {
		note = values.one("reason")
		if note == "" {
			return errors.New("--reason is required")
		}
	}
	// An explicitly empty --pr is not the same as omitting it: the user named a
	// pull request and gave nothing, which is invalid input rather than a review
	// without one.
	if pullRequest, present := values["pr"]; present && strings.TrimSpace(pullRequest[0]) == "" {
		return errors.New("--pr requires a value of the form owner/name#number")
	}
	reviewer, err := decisionMaker(root, values.one("by"))
	if err != nil {
		return err
	}
	// A review must point at content a commit describes, exactly as a
	// verification must: otherwise the judgement names a revision that never
	// held what was reviewed.
	if err := repository.EnsureClean(root, "reviewing"); err != nil {
		return err
	}
	revision, err := repository.Head(root)
	if err != nil {
		return err
	}

	var evidence work.Evidence
	var status work.Status
	var unfinished string
	var unlocked []string
	if err := storage.Update(root, func(state *work.State) error {
		var recordErr error
		evidence, recordErr = state.RecordReview(id, revision, result, reviewer, note, values.one("pr"), now())
		if recordErr != nil {
			return recordErr
		}
		status = state.WorkItemStatus(id)
		unfinished = completionSummary(state, id)
		if status == work.Done {
			unlocked = readyDependents(state, id)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("%s %s: %w", verb, id, err)
	}
	if _, err := fmt.Fprintf(output, "%s %s at %s\n%s %s\nReviewed by: %s (self-asserted; ForgePilot does not authenticate identities)\n",
		evidence.ID, evidence.Result, shortRevision(evidence.Revision), id, status, reviewer); err != nil {
		return err
	}
	for _, dependent := range unlocked {
		if _, err := fmt.Fprintf(output, "%s READY\n", dependent); err != nil {
			return err
		}
	}
	if unfinished != "" {
		_, err = fmt.Fprintln(output, unfinished)
	}
	return err
}

// readyDependents names the work a completion has just unlocked, so the user
// sees the queue move rather than having to go looking for it.
func readyDependents(state *work.State, id string) []string {
	var unlocked []string
	for _, item := range state.WorkItems {
		if item.Status != work.Ready {
			continue
		}
		for _, dependency := range item.DependsOn {
			if dependency == id {
				unlocked = append(unlocked, item.ID)
				break
			}
		}
	}
	return unlocked
}

// completionSummary explains why work that has been approved has not reached
// DONE. Since there is no completion command, this is the only place a user can
// learn what is still missing — staying silent would leave them guessing at an
// approval that appeared to do nothing.
//
// The conditions are checked when an approval is recorded, so lifting the last
// blocker afterwards — resolving a Gate, unblocking the Goal — leaves work whose
// conditions all hold but which is still in REVIEW. That case has to speak
// loudest: there is nothing left to explain, only something left to run.
func completionSummary(state *work.State, id string) string {
	if state.WorkItemStatus(id) == work.Done {
		return ""
	}
	latest, ok := state.LatestReview(id)
	if !ok || latest.Result != work.Approved {
		return ""
	}
	blockers := state.CompletionBlockers(id)
	if len(blockers) == 0 {
		return fmt.Sprintf("Not complete, but every condition now holds: run `forgepilot review approve %s` to finish it", id)
	}
	return "Not complete: " + strings.Join(blockers, "; ")
}

// reviewSummary describes a Work Item's latest Human Review. Work nobody has
// reviewed says so: silence about a missing judgement would read as an untroubled
// one, which is the whole thing this command exists to prevent.
func reviewSummary(state *work.State, id string) string {
	latest, ok := state.LatestReview(id)
	if !ok {
		return "not reviewed"
	}
	// The identity is marked as a claim here too. status is the one place these
	// records are browsed by someone who did not issue the command, so leaving
	// the qualifier off exactly here would be leaving it off where it matters.
	summary := fmt.Sprintf("%s %s at %s by %s (self-asserted)", latest.ID, latest.Result, shortRevision(latest.Revision), latest.Reviewer)
	// A recorded pull request is shown, an absent one passes without comment.
	// Reviewing without one is legal (a review can precede the pull request), so
	// flagging its absence would train the reader to ignore the line.
	if latest.PR != "" {
		summary += fmt.Sprintf(" on %s", latest.PR)
	}
	if latest.Note != "" {
		summary += fmt.Sprintf(": %s", latest.Note)
	}
	return summary
}
