package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

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
		fmt.Fprintf(output, "warning: the canonical check's process group could not be confirmed stopped: %v\n", result.Cleanup)
	}
	return err
}
