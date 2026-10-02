package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func Execute(args []string, cwd string, stdout, stderr io.Writer) int {
	if err := run(args, cwd, stdout); err != nil {
		fmt.Fprintf(stderr, "forgepilot: %v\n", err)
		return 1
	}
	return 0
}

const usageSummary = "usage: forgepilot <init|goal|next|start|reconcile|verify|gate|review|status>"

// Asking what the commands are must not require an initialized repository:
// discovering the CLI is the step before deciding to run it anywhere.
const helpText = `ForgePilot — engineering control plane for AI-assisted work.

` + usageSummary + `

  init                              create .forgepilot state in the current repository
  goal import <plan-path>           create a Goal and its whole DAG from a Goal Plan (JSON); re-import only adds nodes
  goal cancel <goal-id> --reason <text>
  next                              recommend the next legal agent action
  start <work-id>                   move a READY work item to RUNNING
  reconcile --goal <goal-id>        recompute one Goal's PENDING/READY readiness
  verify <work-id> [--snapshot]     verify clean HEAD, or an immutable working-tree snapshot
  gate open --work <work-id> --question <q> --option <o> --option <o> [--reason <text>]
  gate <resolve|cancel> <gate-id>
  review approve <work-id> [--note <text>] [--by <name>]    complete REVIEW work (Goals that require approval)
  review reject <work-id> --reason <text> [--by <name>]     send REVIEW work back to RUNNING
  status [--work <work-id> --summary] print full status, or one Work Item's current summary

ForgePilot does not replace PraxisBound or your coding agent. Nothing here makes
a network request.
`

func run(args []string, cwd string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New(usageSummary)
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprint(output, helpText)
		return err
	}
	if args[0] == "init" {
		if len(args) != 1 {
			return errors.New("usage: forgepilot init")
		}
		if err := storage.Init(cwd); err != nil {
			return err
		}
		_, err := fmt.Fprintf(output, "Initialized ForgePilot in %s\n", cwd)
		return err
	}
	root, err := storage.FindRoot(cwd)
	if err != nil {
		return err
	}
	switch args[0] {
	case "goal":
		return goal(args[1:], cwd, root, output)
	case "next":
		return next(args[1:], root, output)
	case "start":
		return start(args[1:], root, output)
	case "reconcile":
		return reconcile(args[1:], root, output)
	case "verify":
		return verify(args[1:], root, output)
	case "gate":
		return gate(args[1:], root, output)
	case "review":
		return review(args[1:], root, output)
	case "status":
		return status(args[1:], root, output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func goal(args []string, cwd, root string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot goal <import|cancel>")
	}
	switch args[0] {
	case "import":
		return importGoal(args[1:], cwd, root, output)
	case "cancel":
		return cancelGoal(args[1:], root, output)
	default:
		return fmt.Errorf("unknown goal subcommand %q", args[0])
	}
}

// cancelGoal ends a Goal that will not be done. It needs a reason: a status
// saying a Goal stopped without saying why is the record this is meant to avoid.
// A Goal has no other command: it completes by itself when its last Work Item is
// DONE.
func cancelGoal(args []string, root string, output io.Writer) error {
	const usage = "usage: forgepilot goal cancel <goal-id> --reason <text>"
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return errors.New(usage)
	}
	id := args[0]
	values, err := flags(args[1:], map[string]bool{"reason": false})
	if err != nil {
		return err
	}
	reason := values.one("reason")
	if reason == "" {
		return errors.New("--reason is required")
	}
	if err := storage.Update(root, func(state *work.State) error {
		return state.CancelGoal(id, reason, now())
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Goal %s %s\n", id, work.GoalCancelled)
	return err
}

func next(args []string, root string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: forgepilot next")
	}
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	repositoryState, err := nextRepositoryState(&state, root)
	if err != nil {
		return err
	}
	action := state.ActionableNext(repositoryState)
	switch action.Kind {
	case work.NextActionNone:
		_, err = fmt.Fprintln(output, "No actionable work.")
	case work.NextActionGoalCompleted:
		_, err = fmt.Fprintf(output, "Goal %s is completed: every Work Item is DONE.\n", action.Goal.ID)
	case work.NextActionWaitHumanReview, work.NextActionWaitGate, work.NextActionWaitGoal:
		_, err = fmt.Fprintf(output, "No agent-actionable work.\n\nWaiting: %s\nReason: %s\n", action.Item.ID, action.Reason)
	default:
		_, err = fmt.Fprintf(output, "Next: %s\nState: %s\nGoal: %s\nStory: %s\nAction: %s\nReason: %s\n",
			action.Item.ID, action.Item.Status, action.Item.GoalID, action.Item.StoryRef,
			nextActionText(&state, action), action.Reason)
	}
	return err
}

// nextRepositoryState gathers repository facts only when a REVIEW Work Item can
// actually be re-verified or is waiting for a Human Review. READY and RUNNING
// recommendations still work in a repository without a commit, just as next
// did before candidate-aware selection existed.
func nextRepositoryState(state *work.State, root string) (work.RepositoryState, error) {
	return app.CandidateFacts(context.Background(), state, root)
}

// reconcile writes the readiness the dependency rule implies for one Goal. It is
// the explicit command that recovers a queue whose persisted PENDING disagrees
// with it; it appends no Evidence and answers no Gate.
func reconcile(args []string, root string, output io.Writer) error {
	values, err := flags(args, map[string]bool{"goal": false})
	if err != nil {
		return err
	}
	id := values.one("goal")
	if id == "" {
		return errors.New("usage: forgepilot reconcile --goal <goal-id>")
	}
	changes, err := app.ReconcileGoal(root, id, now)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		_, err = fmt.Fprintf(output, "Goal %s readiness unchanged\n", id)
		return err
	}
	if _, err := fmt.Fprintf(output, "Goal %s reconciled\n", id); err != nil {
		return err
	}
	for _, change := range changes {
		if _, err := fmt.Fprintf(output, "%s %s -> %s\n", change.ItemID, change.From, change.To); err != nil {
			return err
		}
	}
	return nil
}

func nextActionText(state *work.State, action work.NextAction) string {
	switch action.Kind {
	case work.NextActionResume:
		return "resume implementation"
	case work.NextActionRepair:
		return "repair implementation and verify again"
	case work.NextActionReverify:
		command := fmt.Sprintf("forgepilot verify %s", action.Item.ID)
		if verification, ok := state.LatestVerification(action.Item.ID); ok && verification.CandidateKind == work.SnapshotCandidate {
			command += " --snapshot"
		}
		return command
	case work.NextActionStart:
		return fmt.Sprintf("forgepilot start %s", action.Item.ID)
	case work.NextActionReconcile:
		// The recommendation names the Goal because readiness is reconciled a Goal
		// at a time; the Work Item it is about is already on the Next: line.
		return fmt.Sprintf("forgepilot reconcile --goal %s", action.Item.GoalID)
	default:
		return ""
	}
}

func start(args []string, root string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: forgepilot start <work-id>")
	}
	if err := app.StartWork(root, args[0], now); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "%s RUNNING\n", args[0])
	return err
}

func status(args []string, root string, output io.Writer) error {
	if len(args) != 0 {
		return statusSummary(args, root, output)
	}
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	// Staleness needs the current revision, but a repository without one is not
	// an error for a query: report what is known and omit the comparison.
	revision, _ := repository.Head(context.Background(), root)
	digest := ""
	for _, item := range state.WorkItems {
		latest, ok := state.LatestVerification(item.ID)
		if item.Status != work.Done && ok && latest.CandidateKind == work.SnapshotCandidate {
			if workspace, inspectErr := repository.InspectSnapshot(context.Background(), root); inspectErr == nil {
				revision, digest = workspace.BaseRevision, workspace.Digest
			}
			break
		}
	}
	for _, goal := range state.Goals {
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
			// A Verification Run whose process is gone is reported, not repaired:
			// status is a pure query, and reclaiming it would be a write.
			note := ""
			if item.CurrentRun != nil && !storage.VerificationRunning(root, item.ID) {
				// verify reclaims an abandoned run before anything can refuse the
				// command, so this instruction works even when the work is blocked.
				note = " (verifier is gone; run forgepilot verify to recover)"
			}
			if _, err := fmt.Fprintf(output, "  %s %s %s%s\n    %s\n", item.ID, item.Status, item.StoryRef, note,
				verificationSummary(&state, item.ID, revision, digest)); err != nil {
				return err
			}
			if goal.RequireApproval {
				if _, err := fmt.Fprintf(output, "    %s\n", reviewSummary(&state, item.ID)); err != nil {
					return err
				}
			}
			for _, line := range gateSummary(&state, item.ID) {
				if _, err := fmt.Fprintf(output, "    %s\n", line); err != nil {
					return err
				}
			}
		}
	}
	if next, ok := state.Next(); ok {
		_, err = fmt.Fprintf(output, "Next: %s\n", next.ID)
	} else {
		_, err = fmt.Fprintln(output, "Next: none")
	}
	return err
}

const statusUsage = "usage: forgepilot status [--work <work-id> --summary]"

// statusSummary deliberately accepts only the paired selectors. A bare
// --work would look like a supported filtered version of the full history,
// while this command's contract is explicitly a current-state summary.
func statusSummary(args []string, root string, output io.Writer) error {
	var id string
	var hasWork, hasSummary bool
	for len(args) > 0 {
		switch args[0] {
		case "--work":
			if hasWork || len(args) < 2 || args[1] == "" || strings.HasPrefix(args[1], "--") {
				return errors.New(statusUsage)
			}
			id, hasWork = args[1], true
			args = args[2:]
		case "--summary":
			if hasSummary {
				return errors.New(statusUsage)
			}
			hasSummary = true
			args = args[1:]
		default:
			return errors.New(statusUsage)
		}
	}
	if !hasWork || !hasSummary {
		return errors.New(statusUsage)
	}

	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	// Resolve the Work Item before Git so an unknown ID always has the documented
	// domain error, not an unrelated repository error.
	summary, err := state.WorkSummary(id, work.RepositoryState{})
	if err != nil {
		return err
	}
	repositoryState := work.RepositoryState{}
	if summary.HasVerification && summary.Item.Status != work.Done {
		if summary.Verification.CandidateKind == work.SnapshotCandidate {
			workspace, inspectErr := repository.InspectSnapshot(context.Background(), root)
			if inspectErr != nil {
				return inspectErr
			}
			repositoryState = work.RepositoryState{Revision: workspace.BaseRevision, SnapshotDigest: workspace.Digest}
		} else {
			revision, headErr := repository.Head(context.Background(), root)
			if headErr != nil {
				return headErr
			}
			repositoryState.Revision = revision
		}
	}
	summary, err = state.WorkSummary(id, repositoryState)
	if err != nil {
		return err
	}
	return writeWorkSummary(output, summary)
}

func writeWorkSummary(output io.Writer, summary work.WorkItemSummary) error {
	blocking := "none"
	if len(summary.BlockingGates) > 0 {
		ids := make([]string, 0, len(summary.BlockingGates))
		for _, gate := range summary.BlockingGates {
			ids = append(ids, gate.ID)
		}
		blocking = strings.Join(ids, ", ")
	}
	_, err := fmt.Fprintf(output, "%s %s\nGoal: %s %s\nApproval required: %s\nStory: %s\nVerification: %s\nReview: %s\nBlocking gates: %s\nCompletion: %s\n",
		summary.Item.ID, summary.Item.Status,
		summary.Goal.ID, summary.Goal.Status,
		yesNo(summary.Goal.RequireApproval),
		summary.Item.StoryRef,
		workVerificationSummary(summary),
		workReviewSummary(summary),
		blocking,
		summary.Completion)
	return err
}

func workVerificationSummary(summary work.WorkItemSummary) string {
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

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

type flagValues map[string][]string

func flags(args []string, allowed map[string]bool) (flagValues, error) {
	values := flagValues{}
	for len(args) > 0 {
		if !strings.HasPrefix(args[0], "--") {
			return nil, fmt.Errorf("unexpected argument %q", args[0])
		}
		name := strings.TrimPrefix(args[0], "--")
		repeatable, ok := allowed[name]
		if !ok {
			return nil, fmt.Errorf("unknown flag --%s", name)
		}
		if len(args) < 2 || strings.HasPrefix(args[1], "--") {
			return nil, fmt.Errorf("--%s requires a value", name)
		}
		if len(values[name]) > 0 && !repeatable {
			return nil, fmt.Errorf("--%s may only be specified once", name)
		}
		values[name] = append(values[name], args[1])
		args = args[2:]
	}
	return values, nil
}

func (f flagValues) one(name string) string {
	if values := f[name]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func (f flagValues) all(name string) []string { return append([]string(nil), f[name]...) }
func now() time.Time                          { return time.Now().UTC() }

// verificationSummary describes a Work Item's latest Verification Evidence. Work
// that has never been verified says so explicitly: silence would read as approval.
func verificationSummary(state *work.State, id, revision, digest string) string {
	latest, ok := state.LatestVerification(id)
	if !ok {
		return "not verified"
	}
	summary := fmt.Sprintf("%s %s at %s", latest.ID, latest.Result, shortRevision(latest.Revision))
	if latest.CandidateKind == work.SnapshotCandidate {
		summary = fmt.Sprintf("%s %s snapshot %s", latest.ID, latest.Result, shortRevision(latest.Revision))
	}
	if state.CandidateStale(id, revision, digest) {
		if latest.CandidateKind == work.SnapshotCandidate {
			summary += " (stale; workspace no longer matches verified snapshot)"
		} else {
			summary += fmt.Sprintf(" (stale; HEAD is now %s)", shortRevision(revision))
		}
	}
	return summary
}

// abbreviatedRevisionLength is how much of a commit SHA the CLI shows. It is
// long enough to identify a commit by eye and short enough to keep a status line
// readable.
const abbreviatedRevisionLength = 12

func shortRevision(revision string) string {
	if len(revision) > abbreviatedRevisionLength {
		return revision[:abbreviatedRevisionLength]
	}
	return revision
}
