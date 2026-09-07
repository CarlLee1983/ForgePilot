package main

import (
	"os"

	"github.com/carl/forgepilot/internal/cli"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString("forgepilot: get working directory: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(cli.Execute(os.Args[1:], cwd, os.Stdout, os.Stderr))
}
