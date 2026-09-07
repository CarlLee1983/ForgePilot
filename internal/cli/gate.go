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

func gate(args []string, root string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot gate <open|resolve|cancel>")
	}
	switch args[0] {
	case "open":
		return openGate(args[1:], root, output)
	case "resolve":
		return resolveGate(args[1:], root, output)
	case "cancel":
		return cancelGate(args[1:], root, output)
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

func resolveGate(args []string, root string, output io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return errors.New("usage: forgepilot gate resolve <gate-id> --option <option> [--note <text>] [--as <identity>]")
	}
	id := args[0]
	values, err := flags(args[1:], map[string]bool{"option": false, "note": false, "as": false})
	if err != nil {
		return err
	}
	if values.one("option") == "" {
		return errors.New("--option is required")
	}
	decidedBy, err := decisionMaker(root, values.one("as"))
	if err != nil {
		return err
	}
	if err := storage.Update(root, func(state *work.State) error {
		return state.ResolveGate(id, values.one("option"), values.one("note"), decidedBy, now())
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s RESOLVED\nChoice: %s\n%s\n", id, values.one("option"), decisionMakerLine(decidedBy))
	return err
}

func cancelGate(args []string, root string, output io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return errors.New("usage: forgepilot gate cancel <gate-id> --reason <text> [--as <identity>]")
	}
	id := args[0]
	values, err := flags(args[1:], map[string]bool{"reason": false, "as": false})
	if err != nil {
		return err
	}
	if values.one("reason") == "" {
		return errors.New("--reason is required")
	}
	decidedBy, err := decisionMaker(root, values.one("as"))
	if err != nil {
		return err
	}
	if err := storage.Update(root, func(state *work.State) error {
		return state.CancelGate(id, values.one("reason"), decidedBy, now())
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s CANCELLED\nReason: %s\n%s\n", id, values.one("reason"), decisionMakerLine(decidedBy))
	return err
}

// decisionMaker resolves who is recorded as having decided: the value given on
// the command line, otherwise whatever Git is configured with. The domain never
// reads Git configuration itself.
func decisionMaker(root, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return repository.ConfiguredIdentity(root)
}

// decisionMakerLine says out loud what the recorded identity is worth. Printing
// the address alone would let a reader take it for an authenticated one.
func decisionMakerLine(identity string) string {
	return fmt.Sprintf("Decided by: %s (self-asserted; ForgePilot does not authenticate identities)", identity)
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
			line += fmt.Sprintf(" -> %s (self-asserted by %s)", gate.Choice, gate.DecidedBy)
		case work.GateCancelled:
			line += fmt.Sprintf(" -> cancelled: %s (self-asserted by %s)", gate.Reason, gate.DecidedBy)
		}
		lines = append(lines, line)
	}
	return lines
}
