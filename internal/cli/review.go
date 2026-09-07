package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/carl/forgepilot/internal/repository"
	"github.com/carl/forgepilot/internal/storage"
	"github.com/carl/forgepilot/internal/work"
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
	verb := strings.ToLower(string(result))
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		if result == work.Rejected {
			return errors.New("usage: forgepilot review reject <work-id> --reason <text> [--as <identity>]")
		}
		return errors.New("usage: forgepilot review approve <work-id> [--note <text>] [--as <identity>]")
	}
	id := args[0]
	allowed := map[string]bool{"as": false, "note": false}
	if result == work.Rejected {
		allowed = map[string]bool{"as": false, "reason": false}
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
	reviewer, err := decisionMaker(root, values.one("as"))
	if err != nil {
		return err
	}
	// A review must point at content a commit describes, exactly as a
	// verification must: otherwise the judgement names a revision that never
	// held what was reviewed.
	if err := repository.EnsureClean(root); err != nil {
		return err
	}
	revision, err := repository.Head(root)
	if err != nil {
		return err
	}

	var evidence work.Evidence
	var status work.Status
	if err := storage.Update(root, func(state *work.State) error {
		var recordErr error
		evidence, recordErr = state.RecordReview(id, revision, result, reviewer, note, now())
		if recordErr != nil {
			return recordErr
		}
		status = state.WorkItemStatus(id)
		return nil
	}); err != nil {
		return fmt.Errorf("%s %s: %w", verb, id, err)
	}
	_, err = fmt.Fprintf(output, "%s %s at %s\n%s %s\nReviewed by: %s (self-asserted; ForgePilot does not authenticate identities)\n",
		evidence.ID, evidence.Result, shortRevision(evidence.Revision), id, status, reviewer)
	return err
}

// reviewSummary describes a Work Item's latest Human Review. Work nobody has
// reviewed says so: silence about a missing judgement would read as an untroubled
// one, which is the whole thing this command exists to prevent.
func reviewSummary(state *work.State, id string) string {
	latest, ok := state.LatestReview(id)
	if !ok {
		return "not reviewed"
	}
	summary := fmt.Sprintf("%s %s at %s by %s", latest.ID, latest.Result, shortRevision(latest.Revision), latest.Reviewer)
	if latest.Note != "" {
		summary += fmt.Sprintf(": %s", latest.Note)
	}
	return summary
}
