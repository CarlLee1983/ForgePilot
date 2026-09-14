package runner

import (
	"fmt"
	"path/filepath"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/process"
)

// judgePending answers the only question a recovery asks: may a new writer
// start on this workspace. It answers "yes" only when the execution can be
// shown to be over, and it never guesses — an identity nobody can check, a pid
// that has been reused, a leader that has exited leaving its group populated
// are all "no". See docs/adr/0020-worker-ownership-is-fail-closed.md and
// docs/adr/0022-pending-cleanup-outlives-the-process.md.
func judgePending(pending PendingExecution) (bool, string) {
	where := pending.Kind
	if pending.WorkItemID != "" {
		where += " for " + pending.WorkItemID
	}
	if pending.Location != "" {
		where += " in " + pending.Location
	}
	because := ""
	if pending.StopReason != "" {
		because = fmt.Sprintf("; it was stopped %s", pending.StopReason)
	}
	if pending.CleanupDetail != "" {
		because += "; its cleanup reported: " + pending.CleanupDetail
	}

	// A record with a full identity can be checked the same way a worker is.
	if pending.Identity.Recorded() {
		liveness, err := agent.Inspect(pending.Identity)
		switch {
		case err != nil:
			return false, fmt.Sprintf("%s cannot be checked (pid %d): %v%s", where, pending.Identity.PID, err, because)
		case liveness == agent.Ours:
			if stopErr := agent.TerminateOwned(pending.Identity); stopErr != nil {
				return false, fmt.Sprintf("%s (pid %d) could not be stopped: %v%s", where, pending.Identity.PID, stopErr, because)
			}
			return true, ""
		case liveness == agent.Gone, liveness == agent.Unrelated:
			// The leader is not ours any more, which says nothing about the rest of
			// its group. Only an empty group is a confirmation.
			if process.Gone(pending.Identity.PGID) {
				return true, ""
			}
			return false, fmt.Sprintf("%s left process group %d with members although pid %d is no longer it; it cannot be signalled without guessing whose it is%s",
				where, pending.Identity.PGID, pending.Identity.PID, because)
		default:
			return false, fmt.Sprintf("%s (pid %d) cannot be confirmed stopped%s", where, pending.Identity.PID, because)
		}
	}

	// No identity, but a group id was observed. An empty group is still a real
	// confirmation; a populated one is not ours to guess about.
	if pending.Identity.PGID > 0 {
		if process.Gone(pending.Identity.PGID) {
			return true, ""
		}
		return false, fmt.Sprintf("%s left process group %d alive%s", where, pending.Identity.PGID, because)
	}

	// Nothing was observed at all. That is what a crash in the launch window
	// looks like from here, and it is indistinguishable from a launch that never
	// happened — so it is refused rather than assumed away.
	return false, fmt.Sprintf("%s was recorded %s and no process identity was ever observed for it, so it cannot be shown to have stopped%s",
		where, pending.Phase, because)
}

// manualRecoveryHint tells a person what to do about a run that cannot be
// settled. It names the file rather than offering a flag: there is deliberately
// no --force, because a bypass is how the one guarantee here gets spent.
func manualRecoveryHint(runID string) string {
	return fmt.Sprintf("; confirm nothing is writing this workspace, then clear the matching entry from %s",
		filepath.Join(".forgepilot", "runs", runID, recordName))
}
