package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/process"
)

// settleExecution is the one safety criterion a recovery has, for every kind of
// execution a run may have left behind. It answers "may a new writer start"
// with nil, and it answers it only when the execution can be shown to be over:
// an identity nobody can check, a pid that has been reused, a leader that has
// exited leaving its group populated are all refusals.
//
// It is one function rather than one per record shape because Worker and
// Pending ask exactly this question and used to answer it differently — Pending
// refused a populated group, Worker read the same fact as "the worker is gone".
// See docs/adr/0020-worker-ownership-is-fail-closed.md and
// docs/adr/0022-pending-cleanup-outlives-the-process.md.
func settleExecution(identity agent.ProcessIdentity) error {
	// A full identity is checked and stopped by the boundary that owns the rule:
	// TerminateOwned signals only a group whose identity still matches, insists
	// on an observably empty group when the pid is gone or reused, and refuses
	// outright when it cannot tell. Restating any of that here is how the two
	// copies came to disagree.
	if identity.Recorded() {
		return agent.TerminateOwned(identity)
	}
	// No identity, but a group id was observed. An empty group is still a real
	// confirmation; a populated one is not ours to guess about.
	if identity.PGID > 0 {
		if process.Gone(identity.PGID) {
			return nil
		}
		return &process.NotSettled{PGID: identity.PGID, Reason: fmt.Sprintf(
			"process group %d still has members and no identity was recorded for it, so it cannot be signalled without guessing whose it is",
			identity.PGID)}
	}
	// Nothing was observed at all. That is what a crash in the launch window
	// looks like from here, and it is indistinguishable from a launch that never
	// happened — so it is refused rather than assumed away. It is the same type
	// as the other two refusals: a caller that one day filters on
	// process.ErrNotSettled must not find this branch falling through it.
	return &process.NotSettled{Reason: "no process identity was ever observed for it, so it cannot be shown to have stopped"}
}

// judgePending answers the only question a recovery asks: may a new writer
// start on this workspace.
func judgePending(pending PendingExecution) (bool, string) {
	err := settleExecution(pending.Identity)
	if err == nil {
		return true, ""
	}
	where := pending.Kind
	if pending.WorkItemID != "" {
		where += " for " + pending.WorkItemID
	}
	if pending.Location != "" {
		where += " in " + pending.Location
	}
	if !pending.Identity.Recorded() && pending.Identity.PGID <= 0 {
		where += " was recorded " + pending.Phase
	}
	because := ""
	if pending.StopReason != "" {
		because = fmt.Sprintf("; it was stopped %s", pending.StopReason)
	}
	if pending.CleanupDetail != "" {
		because += "; its cleanup reported: " + pending.CleanupDetail
	}
	return false, fmt.Sprintf("%s: %v%s", where, err, because)
}

// manualRecoveryHint tells a person what to do about a run that cannot be
// settled. It names the file rather than offering a flag: there is deliberately
// no --force, because a bypass is how the one guarantee here gets spent.
func manualRecoveryHint(runID string) string {
	return fmt.Sprintf("; confirm nothing is writing this workspace, then clear the matching entry from %s",
		filepath.Join(".forgepilot", "runs", runID, recordName))
}

// recover settles this run's own record before anything new is launched: first
// every pending execution it wrote, then the worker entry. The two are separate
// fields because they have different lifetimes and different compatibility
// stories, but they are judged by one criterion — settleExecution — because
// they ask one question, and two implementations of it drifted apart once
// already. See docs/adr/0020-worker-ownership-is-fail-closed.md.
//
// It reports whether this pass refused, which is the caller's cue to stop.
// That verdict is returned rather than read back from record.Stop: a record can
// already carry a RECOVERY_BLOCKED stop from an earlier process — the
// session-cleanup path writes a worker, a pending execution and a stop together
// — and reading that old stop as "this pass refused" kept refusing a workspace
// that had since become confirmable, until somebody ran the command twice.
func (runner *Runner) recover() (bool, error) {
	blocked, err := runner.recoverPending()
	if err != nil || blocked {
		return blocked, err
	}
	worker := runner.record.Worker
	if worker == nil {
		return false, nil
	}
	runner.print("Recovering run %s: settling the worker left behind for %s (pid %d)\n",
		runner.record.RunID, worker.WorkItemID, worker.Identity.PID)
	// The same criterion a pending execution is judged by. A leader that has
	// exited says nothing about the group it left: only an empty group does.
	if err := settleExecution(worker.Identity); err != nil {
		// The worker record is deliberately left in place: clearing it would make
		// the next attempt look clean when nothing has actually been established.
		detail := fmt.Sprintf("the worker for %s (run %s, pid %d) cannot be shown to have stopped writing this workspace: %v",
			worker.WorkItemID, runner.record.RunID, worker.Identity.PID, err)
		detail += fmt.Sprintf("; check that pid and its process group yourself, and once you are sure nothing is writing this tree, clear \"worker\" from %s",
			filepath.Join(".forgepilot", "runs", runner.record.RunID, recordName))
		return true, runner.stopNow(StopRecoveryBlocked, detail)
	}
	runner.print("Recovering run %s: the worker for %s is gone\n", runner.record.RunID, worker.WorkItemID)
	runner.record.Worker = nil
	return false, runner.save()
}

// recoverPending judges each unresolved execution and clears only the ones that
// were shown to be over. Clearing is atomic with the confirmation: the record is
// replaced whole, so a reader sees either the entry or its absence, never a
// half-cleared claim.
// It reports whether it refused, which is the caller's cue to stop — asking the
// record afterwards cannot tell a refusal made here from one made in an earlier
// process.
func (runner *Runner) recoverPending() (bool, error) {
	for _, pending := range runner.record.UnresolvedPending() {
		safe, detail := judgePending(pending)
		if !safe {
			return true, runner.stopNow(StopRecoveryBlocked, detail+manualRecoveryHint(runner.record.RunID))
		}
		runner.print("Recovering run %s: %s is confirmed stopped\n", runner.record.RunID, pending.Kind)
		runner.record.resolvePending(pending.ID)
		if err := runner.save(); err != nil {
			return false, err
		}
	}
	return false, nil
}

// readFacts runs one between-steps Git read with the same protection every
// other external process gets: a pending execution recorded before it starts,
// resolved only when it is confirmed over. These reads are short, but they are
// `read-tree`, `add -A` and `write-tree` against the user's own worktree, so
// they run this repository's clean filters and can leave a child behind exactly
// as a canonical check can. Without this a cleanup nobody could confirm
// vanished from the record, and the next `run` found a workspace that looked
// clear. See docs/adr/0022-pending-cleanup-outlives-the-process.md.
//
// It reports whether the run was stopped, so the caller stops rather than
// classifying a cleanup failure as whatever its own error path would have said
// — a stalled Goal, most often, which is an engineering statement about code
// that did nothing wrong.
func (runner *Runner) readFacts(itemID string, read func(ctx context.Context) error) (bool, error) {
	execution := runner.newFactsExecution()
	defer execution.release()
	pendingID := runner.record.addPending(PendingExecution{
		Kind: KindGit, Phase: PhasePendingStart, WorkItemID: itemID,
		Location: runner.options.Root, ObservedAt: runner.now()})
	if err := runner.save(); err != nil {
		runner.record.resolvePending(pendingID)
		return true, err
	}
	readErr := read(execution.ctx)
	if unsettled := unsettledGit(readErr); unsettled != nil {
		runner.record.resolvePending(pendingID)
		pgid, _ := process.UnsettledGroup(unsettled)
		runner.record.addPending(PendingExecution{
			Kind: KindGit, Phase: PhaseCleanupUnconfirmed, WorkItemID: itemID,
			Location: runner.options.Root, Identity: agent.ProcessIdentity{PGID: pgid},
			StopReason: describe(execution.Cause()), CleanupDetail: unsettled.Error(),
			ObservedAt: runner.now()})
		detail := fmt.Sprintf(
			"a Git process group started while reading repository facts could not be confirmed stopped: %v (the read ended %s). Find what is still running under this workspace and stop it before running anything else",
			unsettled, describe(execution.Cause()))
		if saveErr := runner.save(); saveErr != nil {
			return true, fmt.Errorf("%w; the unrecorded cleanup was: %s", saveErr, detail)
		}
		return true, runner.stopNow(StopRecoveryBlocked, detail)
	}
	runner.record.resolvePending(pendingID)
	if err := runner.save(); err != nil {
		return true, err
	}
	// Then the stop this read was cut short by, if there was one. The order is
	// the same everywhere: an unconfirmed cleanup first, because nothing may
	// start after it; then the reason the execution actually ended, recorded when
	// it happened rather than guessed from the error; and only what is left over
	// goes on as an ordinary Git or operational failure. Without this a Ctrl-C
	// during a between-steps read came back as a plain error, which start and
	// reconcile reported as STALLED and the loop reported as exit 1 — three
	// different names for "somebody pressed Ctrl-C".
	// See docs/adr/0021-execution-limits-are-bounded-and-named.md.
	if readErr != nil {
		// A facts read has no timeout of its own — newFactsExecution gives it the
		// run's remaining duration — so a step timeout here is the run's deadline
		// under another name.
		if reason, ok := runner.stopReasonFor(execution.Cause(), StopMaxDuration); ok {
			return true, runner.stopNow(reason, fmt.Sprintf("reading repository facts ended %s: %v",
				describe(execution.Cause()), readErr))
		}
	}
	return false, readErr
}

// unsettledGit reports the unconfirmed-group part of an error, or nil. It is the
// runner-side twin of internal/app's unsettledPart: both exist because a
// cancelled command whose child could not be confirmed gone carries two facts,
// and only one of them means the next step may not start.
func unsettledGit(err error) error {
	if err == nil {
		return nil
	}
	var unsettled *process.NotSettled
	if errors.As(err, &unsettled) {
		return unsettled
	}
	if errors.Is(err, process.ErrNotSettled) {
		return err
	}
	return nil
}
