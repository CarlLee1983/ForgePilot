package main

import (
	"os"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/cli"
)

func main() {
	if err := reexecCanonicalProcessImage(); err != nil {
		os.Stderr.WriteString("forgepilot: " + err.Error() + "\n")
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString("forgepilot: get working directory: " + err.Error() + "\n")
		os.Exit(1)
	}
	resolver, err := runnerGenerationResolver(os.Args[1:])
	if err != nil {
		os.Stderr.WriteString("forgepilot: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(cli.ExecuteWithGenerationResolver(os.Args[1:], cwd, os.Stdout, os.Stderr, resolver))
}

// Pinned authorization and Runner admission need a process-image fact that
// cannot be reconstructed after a Bootstrap upgrade. Read-only previews and
// other local commands remain usable from an unmanaged development binary.
func runnerGenerationResolver(args []string) (app.EngineGenerationResolver, error) {
	if !requiresRunnerGeneration(args) {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	image, err := app.CaptureBootstrapProcessImage(home)
	if err != nil {
		return nil, err
	}
	return app.BootstrapGenerationResolver{ProcessImage: image}, nil
}

func requiresRunnerGeneration(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "execution" {
		return len(args) > 1 && (args[1] == "resume" || args[1] == "authorize" ||
			(args[1] == "retention" && len(args) > 2 && args[2] == "reconcile") ||
			(args[1] == "revise" && len(args) > 2 && args[2] == "authorize"))
	}
	if args[0] != "run" || len(args) < 2 || args[1] == "status" {
		return false
	}
	if args[1] == "resume" {
		return true
	}
	for _, arg := range args[1:] {
		if arg == "--dry-run" {
			return false
		}
	}
	return true
}
