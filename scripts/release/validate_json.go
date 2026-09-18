package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate_json <file>")
		os.Exit(2)
	}

	contents, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !json.Valid(contents) {
		fmt.Fprintf(os.Stderr, "%s is not valid JSON\n", os.Args[1])
		os.Exit(1)
	}
}
