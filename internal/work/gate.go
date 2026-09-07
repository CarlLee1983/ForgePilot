package work

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GateStatus tracks a Gate through its only permitted moves. OPEN is the sole
// non-terminal value: a Gate that has been answered or withdrawn never changes
// again, which is what lets the Gate set stand as history without being copied
// into Evidence.
type GateStatus string

const (
	GateOpen      GateStatus = "OPEN"
	GateResolved  GateStatus = "RESOLVED"
	GateCancelled GateStatus = "CANCELLED"
)

// MinimumGateOptions is the number of choices a Gate must offer. One option is
// not a question, and a Gate with no options is a note rather than a decision
// waiting to be made.
const MinimumGateOptions = 2

// Gate is a question that neither ForgePilot nor an Agent has the authority to
// answer, attached to one Work Item. While any Gate on a Work Item is OPEN that
// item cannot be advanced — blocking is a condition, not a status, so the Work
// Item's own status is left exactly as it was.
//
// DecidedBy holds a self-asserted identity, never an authenticated one: see
// docs/adr/0005-self-asserted-decision-maker.md.
type Gate struct {
	ID         string     `json:"id"`
	WorkItemID string     `json:"work_item_id"`
	Question   string     `json:"question"`
	Options    []string   `json:"options"`
	Rationale  string     `json:"rationale"`
	Status     GateStatus `json:"status"`
	// Choice is the selected option, present only on a RESOLVED Gate.
	Choice string `json:"choice"`
	// Note carries the free-text judgement behind a resolution; Reason carries
	// the required explanation for a cancellation. They are kept apart because
	// one is optional and the other is the whole point of the record.
	Note      string     `json:"note"`
	Reason    string     `json:"reason"`
	DecidedBy string     `json:"decided_by"`
	DecidedAt *time.Time `json:"decided_at"`
	OpenedAt  time.Time  `json:"opened_at"`
}

func validateGates(gates []Gate, nextID int, items map[string]Item) error {
	seen := map[string]bool{}
	maxID := 0
	for _, gate := range gates {
		number, ok := parseGateID(gate.ID)
		if !ok {
			return fmt.Errorf("invalid gate ID %q", gate.ID)
		}
		if seen[gate.ID] {
			return fmt.Errorf("duplicate gate %q", gate.ID)
		}
		seen[gate.ID] = true
		if number > maxID {
			maxID = number
		}
		item, ok := items[gate.WorkItemID]
		if !ok {
			return fmt.Errorf("gate %q refers to unknown work item %q", gate.ID, gate.WorkItemID)
		}
		if strings.TrimSpace(gate.Question) == "" {
			return fmt.Errorf("gate %q has no question", gate.ID)
		}
		if err := validateGateOptions(gate); err != nil {
			return err
		}
		switch gate.Status {
		case GateOpen:
			// A Work Item cannot have reached DONE while a question about it is
			// still open: completion requires every Gate to be closed.
			if item.Status == Done {
				return fmt.Errorf("gate %q is open on completed work item %q", gate.ID, gate.WorkItemID)
			}
			if gate.Choice != "" || gate.Note != "" || gate.Reason != "" || gate.DecidedBy != "" || gate.DecidedAt != nil {
				return fmt.Errorf("gate %q is open but carries a decision", gate.ID)
			}
		case GateResolved:
			if !contains(gate.Options, gate.Choice) {
				return fmt.Errorf("gate %q was resolved with an option it does not offer", gate.ID)
			}
			if gate.Reason != "" {
				return fmt.Errorf("gate %q was resolved but carries a cancellation reason", gate.ID)
			}
		case GateCancelled:
			if strings.TrimSpace(gate.Reason) == "" {
				return fmt.Errorf("gate %q was cancelled without a reason", gate.ID)
			}
			if gate.Choice != "" || gate.Note != "" {
				return fmt.Errorf("gate %q was cancelled but carries a resolution", gate.ID)
			}
		default:
			return fmt.Errorf("gate %q has unknown status %q", gate.ID, gate.Status)
		}
		if gate.Status != GateOpen && (gate.DecidedBy == "" || gate.DecidedAt == nil) {
			return fmt.Errorf("gate %q is closed without a decision maker and time", gate.ID)
		}
	}
	if nextID <= maxID {
		return errors.New("next_gate_id would reuse an ID")
	}
	return nil
}

func validateGateOptions(gate Gate) error {
	if len(gate.Options) < MinimumGateOptions {
		return fmt.Errorf("gate %q offers %d options; at least %d are required", gate.ID, len(gate.Options), MinimumGateOptions)
	}
	offered := map[string]bool{}
	for _, option := range gate.Options {
		if strings.TrimSpace(option) == "" {
			return fmt.Errorf("gate %q has an empty option", gate.ID)
		}
		if offered[option] {
			return fmt.Errorf("gate %q offers %q twice", gate.ID, option)
		}
		offered[option] = true
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func parseGateID(id string) (int, bool) {
	if !strings.HasPrefix(id, "GATE-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "GATE-"))
	return n, err == nil && n > 0 && fmt.Sprintf("GATE-%03d", n) == id
}
