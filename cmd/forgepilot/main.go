package main

import (
	"os"

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
	os.Exit(cli.Execute(os.Args[1:], cwd, os.Stdout, os.Stderr))
}
