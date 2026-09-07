package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/carl/forgepilot/internal/repository"
	"github.com/carl/forgepilot/internal/storage"
	"github.com/carl/forgepilot/internal/work"
)

func Execute(args []string, cwd string, stdout, stderr io.Writer) int {
	if err := run(args, cwd, stdout); err != nil {
		fmt.Fprintf(stderr, "forgepilot: %v\n", err)
		return 1
	}
	return 0
}

func run(args []string, cwd string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot <init|migrate|goal|work|next|start|verify|status>")
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
		return addWork(args[1:], root, output)
	case "next":
		return next(args[1:], root, output)
	case "start":
		return start(args[1:], root, output)
	case "verify":
		return verify(args[1:], root, output)
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
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: forgepilot goal create --id <id> --title <title> [--description <text>]")
	}
	flags, err := flags(args[1:], map[string]bool{"id": false, "title": false, "description": false})
	if err != nil {
		return err
	}
	if flags.one("id") == "" || flags.one("title") == "" {
		return errors.New("--id and --title are required")
	}
	if err := storage.Update(root, func(state *work.State) error {
		return state.AddGoal(flags.one("id"), flags.one("title"), flags.one("description"), root, now())
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Goal %s created\n", flags.one("id"))
	return err
}

func addWork(args []string, root string, output io.Writer) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: forgepilot work add --goal <id> --story <path> [--depends-on <work-id>]")
	}
	flags, err := flags(args[1:], map[string]bool{"goal": false, "story": false, "depends-on": true})
	if err != nil {
		return err
	}
	if flags.one("goal") == "" || flags.one("story") == "" {
		return errors.New("--goal and --story are required")
	}
	story, err := repository.ValidateStory(root, flags.one("story"))
	if err != nil {
		return err
	}
	var added work.Item
	if err := storage.Update(root, func(state *work.State) error {
		var addErr error
		added, addErr = state.AddWork(flags.one("goal"), story, flags.all("depends-on"), now())
		return addErr
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s %s\nStory: %s\n", added.ID, added.Status, added.StoryRef)
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
	item, ok := state.Next()
	if !ok {
		_, err = fmt.Fprintln(output, "No READY work.")
		return err
	}
	revision, _ := repository.Head(root)
	_, err = fmt.Fprintf(output, "Next: %s\nGoal: %s\nStory: %s\nVerification: %s\nReason: earliest READY work\n",
		item.ID, item.GoalID, item.StoryRef, verificationSummary(&state, item.ID, revision))
	return err
}

func start(args []string, root string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: forgepilot start <work-id>")
	}
	if err := storage.Update(root, func(state *work.State) error { return state.Start(args[0], now()) }); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "%s RUNNING\n", args[0])
	return err
}

func status(args []string, root string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: forgepilot status")
	}
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	// Staleness needs the current revision, but a repository without one is not
	// an error for a query: report what is known and omit the comparison.
	revision, _ := repository.Head(root)
	for _, goal := range state.Goals {
		if _, err := fmt.Fprintf(output, "Goal %s %s: %s\n", goal.ID, goal.Status, goal.Title); err != nil {
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
				note = " (runner is gone; run forgepilot verify to recover)"
			}
			if _, err := fmt.Fprintf(output, "  %s %s %s%s\n    %s\n", item.ID, item.Status, item.StoryRef, note, verificationSummary(&state, item.ID, revision)); err != nil {
				return err
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
func verificationSummary(state *work.State, id, revision string) string {
	latest, ok := state.LatestVerification(id)
	if !ok {
		return "not verified"
	}
	summary := fmt.Sprintf("%s %s at %s", latest.ID, latest.Result, shortRevision(latest.Revision))
	if state.Stale(id, revision) {
		summary += fmt.Sprintf(" (stale; HEAD is now %s)", shortRevision(revision))
	}
	return summary
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
