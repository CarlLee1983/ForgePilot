package cli

import (
	"context"
	"errors"
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
	_, err := app.Verify(context.Background(), root, args[0], output, app.VerifyOptions{Snapshot: len(args) == 2, Now: now})
	return err
}
