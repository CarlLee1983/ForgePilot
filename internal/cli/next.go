package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const nextUsage = "usage: forgepilot next [--json]"

// nextJSON is the stable machine-readable shape of `next --json`. Every field is
// always present: absent values are empty strings and Waiting is an empty list,
// so a reader never has to distinguish a missing key from an empty one.
type nextJSON struct {
	Action      string        `json:"action"`
	WorkID      string        `json:"work_id"`
	GoalID      string        `json:"goal_id"`
	StoryRef    string        `json:"story_ref"`
	Status      string        `json:"status"`
	Instruction string        `json:"instruction"`
	Reason      string        `json:"reason"`
	Waiting     []waitingJSON `json:"waiting"`
}

type waitingJSON struct {
	Kind   string `json:"kind"`
	WorkID string `json:"work_id"`
	GoalID string `json:"goal_id"`
	GateID string `json:"gate_id"`
	Reason string `json:"reason"`
}

// next recommends one action. It only reads: state is loaded without the
// transaction lock and nothing is saved.
func next(args []string, root string, output io.Writer) error {
	asJSON := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--json":
		asJSON = true
	default:
		return errors.New(nextUsage)
	}
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	repositoryState, err := app.CandidateFacts(context.Background(), &state, root)
	if err != nil {
		return err
	}
	action := state.ActionableNext(repositoryState)
	if asJSON {
		return writeJSON(output, newNextJSON(&state, action))
	}
	return writeNextText(output, &state, action)
}

func newNextJSON(state *work.State, action work.NextAction) nextJSON {
	result := nextJSON{Action: string(action.Kind), Reason: action.Reason, Waiting: []waitingJSON{}}
	if action.Item.ID != "" {
		result.WorkID, result.GoalID, result.StoryRef = action.Item.ID, action.Item.GoalID, action.Item.StoryRef
		result.Status = state.DisplayStatus(action.Item.ID)
		result.Instruction = nextActionText(state, action)
	}
	if action.Goal.ID != "" {
		result.GoalID = action.Goal.ID
	}
	for _, waiting := range action.Waiting {
		result.Waiting = append(result.Waiting, waitingJSON{Kind: string(waiting.Kind), WorkID: waiting.ItemID, GoalID: waiting.GoalID, GateID: waiting.GateID, Reason: waiting.Reason})
	}
	return result
}

func writeNextText(output io.Writer, state *work.State, action work.NextAction) error {
	var err error
	switch action.Kind {
	case work.NextActionNone:
		_, err = fmt.Fprintln(output, "No actionable work.")
	case work.NextActionGoalCompleted:
		_, err = fmt.Fprintf(output, "Goal %s is completed: every Work Item is DONE.\n", action.Goal.ID)
	case work.NextActionGoalCancelled:
		_, err = fmt.Fprintf(output, "Goal %s is cancelled (%s). No actionable work.\n", action.Goal.ID, action.Reason)
	case work.NextActionWait:
		if _, err = fmt.Fprint(output, "No agent-actionable work.\n"); err != nil {
			return err
		}
		for _, waiting := range action.Waiting {
			if _, err = fmt.Fprintf(output, "\nWaiting: %s\nReason: %s\n", waiting.ItemID, waiting.Reason); err != nil {
				return err
			}
		}
	default:
		_, err = fmt.Fprintf(output, "Next: %s\nState: %s\nGoal: %s\nStory: %s\nAction: %s\nReason: %s\n",
			action.Item.ID, state.DisplayStatus(action.Item.ID), action.Item.GoalID, action.Item.StoryRef,
			nextActionText(state, action), action.Reason)
	}
	return err
}

func nextActionText(state *work.State, action work.NextAction) string {
	switch action.Kind {
	case work.NextActionResume:
		return "resume implementation"
	case work.NextActionRepair:
		return "repair implementation and verify again"
	case work.NextActionReverify, work.NextActionRecover:
		command := fmt.Sprintf("forgepilot verify %s", action.Item.ID)
		if snapshotCandidate(state, action.Item) {
			command += " --snapshot"
		}
		return command
	case work.NextActionStart:
		return fmt.Sprintf("forgepilot start %s", action.Item.ID)
	default:
		return ""
	}
}

// snapshotCandidate reports whether verifying the item again should use
// --snapshot: its interrupted run, or else its latest Verification, was one.
func snapshotCandidate(state *work.State, item work.Item) bool {
	if item.CurrentRun != nil {
		return item.CurrentRun.CandidateKind == work.SnapshotCandidate
	}
	verification, ok := state.LatestVerification(item.ID)
	return ok && verification.CandidateKind == work.SnapshotCandidate
}

func writeJSON(output io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s\n", encoded)
	return err
}
