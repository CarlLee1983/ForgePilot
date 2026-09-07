package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/carl/forgepilot/internal/repository"
	"github.com/carl/forgepilot/internal/storage"
	"github.com/carl/forgepilot/internal/work"
)

func verify(args []string, root string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: forgepilot verify <work-id>")
	}
	id := args[0]

	// Refuse before touching anything: a project with no canonical check or a
	// worktree that HEAD does not describe cannot be verified, and neither is a
	// verification failure, so neither may leave Evidence behind.
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	if err := state.Verifiable(id); err != nil {
		return err
	}
	if err := repository.EnsureCanonicalCheck(root); err != nil {
		return err
	}
	if err := repository.EnsureClean(root); err != nil {
		return err
	}
	revision, err := repository.Head(root)
	if err != nil {
		return err
	}

	worktree := filepath.Join(root, ".forgepilot", "worktrees", fmt.Sprintf("%s-%s", id, revision[:min(len(revision), 12)]))
	if err := repository.PruneWorktrees(root); err != nil {
		return err
	}
	if err := repository.AddWorktree(root, worktree, revision); err != nil {
		return err
	}
	exitCode, runOutput, runErr := repository.RunCanonicalCheck(worktree)
	if removeErr := repository.RemoveWorktree(root, worktree); removeErr != nil && runErr == nil {
		runErr = removeErr
	}
	if runErr != nil {
		return runErr
	}

	var evidence work.Evidence
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.Verifiable(id); err != nil {
			return err
		}
		var recordErr error
		evidence, recordErr = state.RecordVerification(id, revision, repository.CanonicalCommand, exitCode, now())
		return recordErr
	}); err != nil {
		return err
	}

	item := "?"
	if state, err := storage.Load(root); err == nil {
		for _, candidate := range state.WorkItems {
			if candidate.ID == id {
				item = string(candidate.Status)
			}
		}
	}
	if evidence.Result != work.Pass {
		fmt.Fprint(output, runOutput)
	}
	_, err = fmt.Fprintf(output, "%s %s at %s\n%s %s\n", evidence.ID, evidence.Result, evidence.Revision, id, item)
	return err
}
