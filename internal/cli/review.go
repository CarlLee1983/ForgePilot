package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/CarlLee1983/ForgePilot/internal/app"
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
			return errors.New("usage: forgepilot review reject <work-id> --reason <text> [--by <identity>]")
		}
		return errors.New("usage: forgepilot review approve <work-id> [--note <text>] [--by <identity>]")
	}
	id := args[0]
	allowed := map[string]bool{"by": false, "note": false}
	if result == work.Rejected {
		allowed = map[string]bool{"by": false, "reason": false}
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
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	if err := state.Reviewable(id); err != nil {
		return fmt.Errorf("%s %s: %w", verb, id, err)
	}
	reviewer, err := decisionMaker(root, values.one("by"))
	if err != nil {
		return err
	}
	latest, hasVerification := state.LatestVerification(id)
	expectedVerificationID := ""
	if hasVerification {
		expectedVerificationID = latest.ID
	}
	currentRevision, currentDigest := "", ""
	if hasVerification && latest.CandidateKind == work.SnapshotCandidate {
		workspace, inspectErr := repository.InspectSnapshot(context.Background(), root)
		if inspectErr != nil {
			return inspectErr
		}
		currentRevision, currentDigest = workspace.BaseRevision, workspace.Digest
	} else {
		// Legacy COMMIT review remains strict clean-HEAD review.
		if cleanErr := repository.EnsureClean(context.Background(), root, "reviewing"); cleanErr != nil {
			return cleanErr
		}
		currentRevision, err = repository.Head(context.Background(), root)
		if err != nil {
			return err
		}
	}

	var evidence work.Evidence
	var status work.Status
	var completion []string
	if err := storage.Update(root, func(state *work.State) error {
		candidate, resolveErr := state.ResolveReviewCandidate(id, expectedVerificationID, currentRevision, currentDigest)
		if resolveErr != nil {
			return resolveErr
		}
		var recordErr error
		evidence, recordErr = state.RecordCandidateReview(id, candidate, result, reviewer, note, now())
		if recordErr != nil {
			return recordErr
		}
		status = state.WorkItemStatus(id)
		completion = app.CompletionLines(state, id)
		return nil
	}); err != nil {
		return fmt.Errorf("%s %s: %w", verb, id, err)
	}
	if _, err := fmt.Fprintf(output, "%s %s at %s\n%s %s\nReviewed by: %s (self-asserted; ForgePilot does not authenticate identities)\n",
		evidence.ID, evidence.Result, shortRevision(evidence.Revision), id, status, reviewer); err != nil {
		return err
	}
	for _, line := range completion {
		if _, err := fmt.Fprintln(output, line); err != nil {
			return err
		}
	}
	return nil
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
	if latest.Note != "" {
		summary += fmt.Sprintf(": %s", latest.Note)
	}
	return summary
}
