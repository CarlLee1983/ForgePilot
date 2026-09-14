package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/CarlLee1983/ForgePilot/internal/app"
)

// verify is a thin shell over the shared orchestration in internal/app. The
// Runner calls the same function, so neither grows its own copy of the
// verification sequence. Every refusal and operational failure still reaches
// the user as the same message and the same exit code as before.
func verify(args []string, root string, output io.Writer) error {
	if len(args) < 1 || len(args) > 2 || (len(args) == 2 && args[1] != "--snapshot") {
		return errors.New("usage: forgepilot verify <work-id> [--snapshot]")
	}
	// No deadline: ADR-0004 decided a Verification Run has no built-in time
	// limit, and sharing a service with the Runner is not a reason to give this
	// command the Runner's ceilings.
	result, err := app.Verify(context.Background(), root, args[0], output, app.VerifyOptions{Snapshot: len(args) == 2, Now: now})
	if result.Cleanup != nil {
		// Reported, not turned into a verdict. The command is over and owns
		// nothing further, so the PASS or FAIL it just printed still decides its
		// exit code; what the user needs is to know something may still be running.
		// The stage is read back rather than assumed: an unconfirmed group can come
		// from reclaiming an abandoned run, a preflight, the check itself, the
		// removal of the checkout afterwards or the facts read that follows it, and
		// naming the wrong one sends the user looking in the wrong place.
		fmt.Fprintf(output, "warning: %s could not confirm the process group it stopped: %v\n",
			unresolvedStages(result.Unresolved), result.Cleanup)
	}
	return err
}

// unresolvedStages names the stages that reported an unconfirmed group, in the
// vocabulary internal/app already publishes.
func unresolvedStages(unresolved []app.Unresolved) string {
	if len(unresolved) == 0 {
		return "this verification"
	}
	names := make([]string, 0, len(unresolved))
	for _, entry := range unresolved {
		names = append(names, entry.Kind)
	}
	return strings.Join(names, ", ")
}
