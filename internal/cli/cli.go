package cli

import (
	"context"
	"encoding/json"
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
	err := run(args, cwd, stdout)
	if err == nil {
		return 0
	}
	// A run that stops for a Gate, a budget or a goal review has not failed: it
	// has finished doing what it may legally do. Its exit code says which, and
	// there is nothing to print as an error.
	var status *exitStatus
	if errors.As(err, &status) {
		if status.err != nil {
			fmt.Fprintf(stderr, "forgepilot: %v\n", status.err)
		}
		return status.code
	}
	fmt.Fprintf(stderr, "forgepilot: %v\n", err)
	return 1
}

const usageSummary = "usage: forgepilot <init|migrate|goal|work|next|start|reconcile|verify|run|gate|review|status>"

// Asking what the commands are must not require an initialized repository:
// discovering the CLI is the step before deciding to run it anywhere.
const helpText = `ForgePilot — engineering control plane for AI-assisted work.

` + usageSummary + `

  init                              create .forgepilot state in the current repository
  migrate                           upgrade state written by an older binary
  goal create --id <id> --title <t> [--review-policy <work-item|goal>] [--json]
  goal <block|unblock|complete|cancel> <goal-id>
  work add --goal <id> --story <path> [--depends-on <work-id>] [--external-ref <ref>] [--json]
  work list --goal <id> --json     list one Goal's Work Items for machine use
  next                              recommend the next legal agent action
  start <work-id>                   move a READY work item to RUNNING
  reconcile --goal <goal-id>        recompute one Goal's PENDING/READY readiness
  verify <work-id> [--snapshot]     verify clean HEAD, or an immutable working-tree snapshot
  run --goal <goal-id> --runtime <codex|fake> --snapshot [--dry-run]
                                    drive one GOAL-policy goal until a person is needed
  run status <run-id> [--json]      what that run concluded, and the goal's readiness now
  run resume <run-id>               continue a stopped run without resetting its budget
  gate open --work <work-id> --question <q> --option <o> --option <o> [--reason <text>]
  gate <resolve|cancel> <gate-id>
  review request <work-id>            submit a passing WORK_ITEM candidate for human review
  review <approve|reject> <work-id> [--pr <owner/name#number>]
  status [--work <work-id> --summary] print full status, or one Work Item's current summary

ForgePilot does not replace PraxisBound or your coding agent. Nothing here makes
a network request; the run command launches the local coding CLI you name, and
that CLI may contact a model service of its own.
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
	case "migrate":
		return migrate(args[1:], root, output)
	case "goal":
		return goal(args[1:], root, output)
	case "work":
		return workCommand(args[1:], root, output)
	case "next":
		return next(args[1:], root, output)
	case "start":
		return start(args[1:], root, output)
	case "reconcile":
		return reconcile(args[1:], root, output)
	case "verify":
		return verify(args[1:], root, output)
	case "run":
		return runCommand(args[1:], root, output)
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

func migrate(args []string, root string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: forgepilot migrate")
	}
	upgraded, err := storage.Migrate(root)
	if err != nil {
		return err
	}
	if !upgraded {
		_, err = fmt.Fprintf(output, "State is already at schema version %d; nothing to migrate.\n", work.SchemaVersion)
		return err
	}
	_, err = fmt.Fprintf(output, "Migrated state to schema version %d.\n", work.SchemaVersion)
	return err
}

func goal(args []string, root string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot goal <create|block|unblock|complete|cancel|preflight>")
	}
	switch args[0] {
	case "preflight":
		return goalPreflight(args[1:], root, output)
	case "create":
		return createGoal(args[1:], root, output)
	case "block":
		return changeGoal(args[1:], root, output, "block", work.GoalBlocked)
	case "unblock":
		return changeGoal(args[1:], root, output, "unblock", work.GoalActive)
	case "complete":
		return changeGoal(args[1:], root, output, "complete", work.GoalCompleted)
	case "cancel":
		return changeGoal(args[1:], root, output, "cancel", work.GoalCancelled)
	default:
		return fmt.Errorf("unknown goal subcommand %q", args[0])
	}
}

func createGoal(args []string, root string, output io.Writer) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	flags, err := flags(args, map[string]bool{"id": false, "title": false, "description": false, "review-policy": false})
	if err != nil {
		return err
	}
	if flags.one("id") == "" || flags.one("title") == "" {
		return errors.New("--id and --title are required")
	}
	policy := work.ReviewPerWorkItem
	switch flags.one("review-policy") {
	case "", "work-item":
	case "goal":
		policy = work.ReviewPerGoal
	default:
		return errors.New("--review-policy must be work-item or goal")
	}
	createdAt := now()
	created := work.Goal{ID: flags.one("id"), Title: flags.one("title"), Description: flags.one("description"), Repository: root, Status: work.GoalActive, ReviewPolicy: policy, CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := storage.Update(root, func(state *work.State) error {
		return state.AddGoalWithReviewPolicy(created.ID, created.Title, created.Description, root, policy, createdAt)
	}); err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(output, goalCreateJSON{FormatVersion: jsonFormatVersion, Goal: encodeGoal(created)})
	}
	_, err = fmt.Fprintf(output, "Goal %s created\n", flags.one("id"))
	return err
}

// changeGoal drives the four lifecycle moves. Only the two that stop a Goal take
// a reason: a status saying a Goal stopped without saying why is the record this
// is meant to avoid.
func changeGoal(args []string, root string, output io.Writer, action string, target work.GoalStatus) error {
	needsReason := target == work.GoalBlocked || target == work.GoalCancelled
	usage := fmt.Sprintf("usage: forgepilot goal %s <goal-id>", action)
	if needsReason {
		usage += " --reason <text>"
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return errors.New(usage)
	}
	id := args[0]
	allowed := map[string]bool{}
	if needsReason {
		allowed["reason"] = false
	}
	values, err := flags(args[1:], allowed)
	if err != nil {
		return err
	}
	reason := values.one("reason")
	if needsReason && reason == "" {
		return errors.New("--reason is required")
	}
	if err := storage.Update(root, func(state *work.State) error {
		switch target {
		case work.GoalBlocked:
			return state.BlockGoal(id, reason, now())
		case work.GoalActive:
			repositoryState, err := app.CandidateFacts(context.Background(), state, root)
			if err != nil {
				return fmt.Errorf("resolve current Candidate before unblocking: %w", err)
			}
			return state.UnblockGoalWithRepository(id, repositoryState, now())
		case work.GoalCompleted:
			return state.CompleteGoal(id, now())
		default:
			return state.CancelGoal(id, reason, now())
		}
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Goal %s %s\n", id, target)
	return err
}

func addWork(args []string, root string, output io.Writer) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: forgepilot work add --goal <id> --story <path> [--depends-on <work-id>] [--external-ref <ref>] [--json]")
	}
	args, jsonOutput, err := takeJSONFlag(args[1:])
	if err != nil {
		return err
	}
	flags, err := flags(args, map[string]bool{"goal": false, "story": false, "depends-on": true, "external-ref": false})
	if err != nil {
		return err
	}
	if flags.one("goal") == "" || flags.one("story") == "" {
		return errors.New("--goal and --story are required")
	}
	externalRef, hasExternalRef := flags.one("external-ref"), len(flags["external-ref"]) > 0
	if hasExternalRef && (externalRef == "" || strings.TrimSpace(externalRef) != externalRef) {
		return errors.New("--external-ref must not be blank or have surrounding whitespace")
	}
	result, err := app.AddWork(context.Background(), root, app.WorkAddRequest{
		GoalID: flags.one("goal"), StoryRef: flags.one("story"), Dependencies: flags.all("depends-on"),
		ExternalRef: externalRef,
	}, now)
	if err != nil {
		return err
	}
	added, created := result.Item, result.Created
	if jsonOutput {
		return writeJSON(output, workAddJSON{FormatVersion: jsonFormatVersion, Created: created, WorkItem: encodeWorkItem(added)})
	}
	if _, err := fmt.Fprintf(output, "%s %s\nStory: %s\n", added.ID, added.Status, added.StoryRef); err != nil {
		return err
	}
	// Best-effort: work add already succeeded, so a failure to query git here
	// must not turn a successful command into a failing one. The hint is a
	// courtesy, not a result the caller depends on.
	if uncommitted, hintErr := repository.Uncommitted(context.Background(), root, added.StoryRef); hintErr == nil && uncommitted {
		_, err = fmt.Fprintf(output, "%s is not committed yet;\nuse `forgepilot verify %s --snapshot` to verify the working tree,\nor commit it before commit-mode verification.\n", added.StoryRef, added.ID)
	}
	return err
}

func workCommand(args []string, root string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot work <add|list>")
	}
	switch args[0] {
	case "add":
		return addWork(args, root, output)
	case "list":
		return listWork(args[1:], root, output)
	default:
		return fmt.Errorf("unknown work subcommand %q", args[0])
	}
}

func listWork(args []string, root string, output io.Writer) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	if !jsonOutput {
		return errors.New("usage: forgepilot work list --goal <id> --json")
	}
	flags, err := flags(args, map[string]bool{"goal": false})
	if err != nil {
		return err
	}
	goalID := flags.one("goal")
	if goalID == "" {
		return errors.New("usage: forgepilot work list --goal <id> --json")
	}
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok {
		return fmt.Errorf("unknown goal %q", goalID)
	}
	response := workListJSON{FormatVersion: jsonFormatVersion, Goal: encodeGoal(goal), WorkItems: []workItemJSON{}}
	for _, item := range state.WorkItems {
		if item.GoalID == goalID {
			response.WorkItems = append(response.WorkItems, encodeWorkItem(item))
		}
	}
	return writeJSON(output, response)
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
	case work.NextActionWaitGoalReview:
		_, err = fmt.Fprintf(output, "No agent-actionable work.\n\nWaiting: Goal %s\nReason: %s\n", action.Goal.ID, action.Reason)
	case work.NextActionWaitHumanReview, work.NextActionWaitGate, work.NextActionWaitGoal:
		_, err = fmt.Fprintf(output, "No agent-actionable work.\n\nWaiting: %s\nReason: %s\n", action.Item.ID, action.Reason)
	default:
		_, err = fmt.Fprintf(output, "Next: %s\nState: %s\nGoal: %s\nStory: %s\nAction: %s\nReason: %s\n",
			action.Item.ID, action.Item.Status, action.Item.GoalID, action.Item.StoryRef,
			nextActionText(&state, action), action.Reason)
	}
	return err
}

// nextRepositoryState gathers Git facts only when a REVIEW or VERIFIED Work Item can
// actually be re-verified or is waiting for a Human Review. READY and RUNNING
// recommendations still work in a repository without a commit, just as next
// did before candidate-aware selection existed.
func nextRepositoryState(state *work.State, root string) (work.RepositoryState, error) {
	return app.CandidateFacts(context.Background(), state, root)
}

// reconcile writes the readiness the current facts imply for one Goal. It is the
// explicit command that recovers a queue whose persisted PENDING outlived the
// condition that caused it; it appends no Evidence, answers no Gate and changes
// no review policy.
func reconcile(args []string, root string, output io.Writer) error {
	values, err := flags(args, map[string]bool{"goal": false})
	if err != nil {
		return err
	}
	id := values.one("goal")
	if id == "" {
		return errors.New("usage: forgepilot reconcile --goal <goal-id>")
	}
	changes, err := app.ReconcileGoal(context.Background(), root, id, now)
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
	if err := app.StartWork(context.Background(), root, args[0], now); err != nil {
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
		if _, err := fmt.Fprintf(output, "  Review policy: %s\n", goal.ReviewPolicy); err != nil {
			return err
		}
		if goal.ReviewPolicy == work.ReviewPerGoal {
			summary, summaryErr := state.GoalSummary(goal.ID, work.RepositoryState{Revision: revision, SnapshotDigest: digest})
			if summaryErr != nil {
				return summaryErr
			}
			if _, err := fmt.Fprintf(output, "  Goal review: %s\n", summary.Completion); err != nil {
				return err
			}
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
				note = " (runner is gone; run forgepilot verify to recover)"
			}
			if _, err := fmt.Fprintf(output, "  %s %s %s%s\n    %s\n    %s\n", item.ID, item.Status, item.StoryRef, note,
				verificationSummary(&state, item.ID, revision, digest), reviewSummary(&state, item.ID, goal.ReviewPolicy)); err != nil {
				return err
			}
			if unfinished := completionSummary(&state, item.ID); unfinished != "" {
				if _, err := fmt.Fprintf(output, "    %s\n", unfinished); err != nil {
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
	if next, ok := state.NextWithRepository(work.RepositoryState{Revision: revision, SnapshotDigest: digest}); ok {
		_, err = fmt.Fprintf(output, "Next: %s\n", next.ID)
	} else {
		_, err = fmt.Fprintln(output, "Next: none")
	}
	return err
}

const jsonFormatVersion = "forgepilot.cli/v1"

// The JSON shapes below are a deliberately small public inventory contract.
// They do not mirror the durable State: run paths, Evidence, and other internal
// details remain available only through their own product projections.
type goalJSON struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Status       work.GoalStatus   `json:"status"`
	ReviewPolicy work.ReviewPolicy `json:"review_policy"`
}

type workItemJSON struct {
	ID          string      `json:"id"`
	GoalID      string      `json:"goal_id"`
	StoryRef    string      `json:"story_ref"`
	ExternalRef *string     `json:"external_ref"`
	Status      work.Status `json:"status"`
	DependsOn   []string    `json:"depends_on"`
}

type goalCreateJSON struct {
	FormatVersion string   `json:"format_version"`
	Goal          goalJSON `json:"goal"`
}

type workAddJSON struct {
	FormatVersion string       `json:"format_version"`
	Created       bool         `json:"created"`
	WorkItem      workItemJSON `json:"work_item"`
}

type workListJSON struct {
	FormatVersion string         `json:"format_version"`
	Goal          goalJSON       `json:"goal"`
	WorkItems     []workItemJSON `json:"work_items"`
}

func writeJSON(output io.Writer, value any) error {
	return json.NewEncoder(output).Encode(value)
}

func encodeGoal(goal work.Goal) goalJSON {
	return goalJSON{ID: goal.ID, Title: goal.Title, Description: goal.Description, Status: goal.Status, ReviewPolicy: goal.ReviewPolicy}
}

func encodeWorkItem(item work.Item) workItemJSON {
	var externalRef *string
	if item.ExternalRef != "" {
		ref := item.ExternalRef
		externalRef = &ref
	}
	return workItemJSON{
		ID: item.ID, GoalID: item.GoalID, StoryRef: item.StoryRef, ExternalRef: externalRef,
		Status: item.Status, DependsOn: append([]string{}, item.DependsOn...),
	}
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
	_, err := fmt.Fprintf(output, "%s %s\nGoal: %s %s\nReview policy: %s\nStory: %s\nVerification: %s\nReview: %s\nBlocking gates: %s\nCompletion: %s\n",
		summary.Item.ID, summary.Item.Status,
		summary.Goal.ID, summary.Goal.Status,
		summary.Goal.ReviewPolicy,
		summary.Item.StoryRef,
		workVerificationSummary(summary),
		workReviewSummary(summary),
		blocking,
		workCompletionSummary(summary))
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
	if summary.Goal.ReviewPolicy == work.ReviewPerGoal {
		return "not applicable (GOAL policy)"
	}
	if !summary.HasReview {
		return "not reviewed"
	}
	return fmt.Sprintf("%s %s at %s", summary.Review.ID, summary.Review.Result, shortRevision(summary.Review.Revision))
}

func workCompletionSummary(summary work.WorkItemSummary) string {
	if summary.ApprovalNeedsRerecord {
		return "awaiting human review (re-approve to complete)"
	}
	return string(summary.Completion)
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
		if len(args) < 2 || (strings.HasPrefix(args[1], "--") && name != "external-ref") {
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

func takeJSONFlag(args []string) ([]string, bool, error) {
	remaining := make([]string, 0, len(args))
	found := false
	for index := 0; index < len(args); {
		arg := args[index]
		if arg != "--json" {
			remaining = append(remaining, arg)
			index++
			// All callers of this helper use flags that require one value. Keep
			// that value paired with its flag so `--title --json text` remains
			// the existing missing-value error rather than becoming valid input.
			if strings.HasPrefix(arg, "--") && index < len(args) {
				remaining = append(remaining, args[index])
				index++
			}
			continue
		}
		if found {
			return nil, false, errors.New("--json may only be specified once")
		}
		found = true
		index++
	}
	return remaining, found, nil
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
