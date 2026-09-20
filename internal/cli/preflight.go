package cli

import (
	"context"
	"errors"
	"io"

	"github.com/CarlLee1983/ForgePilot/internal/app"
)

const goalPreflightUsage = "usage: forgepilot goal preflight --request <path> --json"

func goalPreflight(args []string, root string, output io.Writer) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	if !jsonOutput {
		return errors.New(goalPreflightUsage)
	}
	values, err := flags(args, map[string]bool{"request": false})
	if err != nil {
		return err
	}
	requestPath := values.one("request")
	if requestPath == "" {
		return errors.New(goalPreflightUsage)
	}
	projection, preflightErr := app.PreflightGoalPlanFile(context.Background(), root, requestPath)
	if err := writeJSON(output, projection); err != nil {
		return err
	}
	return preflightErr
}
