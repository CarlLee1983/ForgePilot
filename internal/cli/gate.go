package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/carl/forgepilot/internal/storage"
	"github.com/carl/forgepilot/internal/work"
)

func gate(args []string, root string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot gate <open>")
	}
	switch args[0] {
	case "open":
		return openGate(args[1:], root, output)
	default:
		return fmt.Errorf("unknown gate subcommand %q", args[0])
	}
}

func openGate(args []string, root string, output io.Writer) error {
	values, err := flags(args, map[string]bool{"work": false, "question": false, "option": true, "rationale": false})
	if err != nil {
		return err
	}
	if values.one("work") == "" || values.one("question") == "" {
		return errors.New("--work and --question are required")
	}
	var opened work.Gate
	if err := storage.Update(root, func(state *work.State) error {
		var openErr error
		opened, openErr = state.OpenGate(values.one("work"), values.one("question"), values.all("option"), values.one("rationale"), now())
		return openErr
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s OPEN on %s\n%s\nOptions: %s\n",
		opened.ID, opened.WorkItemID, opened.Question, strings.Join(opened.Options, " | "))
	return err
}

// gateSummary describes a Work Item's Gates: the count that blocks it, then every
// Gate ever opened on it. Closed Gates stay listed because a cancellation that
// nobody could see would be a silent way around a pending decision.
func gateSummary(state *work.State, id string) []string {
	gates := state.GatesFor(id)
	lines := []string{fmt.Sprintf("Gates: %d open", state.OpenGateCount(id))}
	for _, gate := range gates {
		line := fmt.Sprintf("  %s %s %s", gate.ID, gate.Status, gate.Question)
		switch gate.Status {
		case work.GateOpen:
			line += fmt.Sprintf(" [%s]", strings.Join(gate.Options, " | "))
		case work.GateResolved:
			line += fmt.Sprintf(" -> %s (by %s)", gate.Choice, gate.DecidedBy)
		case work.GateCancelled:
			line += fmt.Sprintf(" -> cancelled: %s (by %s)", gate.Reason, gate.DecidedBy)
		}
		lines = append(lines, line)
	}
	return lines
}
