package repository

import (
	"errors"
	"strings"
)

// ConfiguredIdentity reports the identity Git is configured with, which serves
// as the default decision maker so that everyday use needs no extra argument.
//
// What it returns is a claim, never an authentication result: ForgePilot does
// not verify who ran the command, or that it was a person at all. See
// docs/adr/0005-self-asserted-decision-maker.md.
func ConfiguredIdentity(root string) (string, error) {
	output, err := git(root, nil, "config", "--get", "user.email")
	identity := strings.TrimSpace(output)
	if err != nil || identity == "" {
		return "", errors.New("no git user.email is configured; pass --by to state who is deciding")
	}
	return identity, nil
}
