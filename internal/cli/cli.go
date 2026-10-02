package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
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

const usageSummary = "usage: forgepilot <init|goal|next|start|verify|gate|review|status>"

// Asking what the commands are must not require an initialized repository:
// discovering the CLI is the step before deciding to run it anywhere.
const helpText = `ForgePilot — engineering control plane for AI-assisted work.

` + usageSummary + `

  init                              create .forgepilot state in the current repository
  goal import <plan-path>           create a Goal and its whole DAG from a Goal Plan (JSON); re-import only adds nodes
  goal cancel <goal-id> --reason <text>
  next [--json]                     recommend the one next legal action; read-only
  start <work-id>                   move a READY work item to RUNNING (one at a time per workspace)
  verify <work-id> [--snapshot]     verify clean HEAD, or an immutable working-tree snapshot
  gate open --work <work-id> --question <q> --option <o> --option <o> [--reason <text>]
  gate <resolve|cancel> <gate-id>
  review approve <work-id> [--note <text>] [--by <name>]    complete REVIEW work (Goals that require approval)
  review reject <work-id> --reason <text> [--by <name>]     send REVIEW work back to RUNNING
  status [--goal <goal-id>] [--work <work-id>] [--json]    show work, readiness, Evidence, and why unfinished work cannot advance

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
