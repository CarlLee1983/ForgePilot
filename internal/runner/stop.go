package runner

// StopReason names why a run ended. It is the Runner's most load-bearing
// output: "the run ended" says nothing a person can act on, and the exit code
// is derived from this rather than guessed at the call site.
type StopReason string

const (
	// StopAwaitingGoalReview is the successful end of a run. It means every
	// condition the Goal final-review projection checks now holds — not that the
	// Goal is complete, and not that anything was approved.
	StopAwaitingGoalReview StopReason = "AWAITING_GOAL_REVIEW"

	// These need a person or an external change before anything can move.
	StopWaitGate             StopReason = "WAIT_GATE"
	StopWaitGoal             StopReason = "WAIT_GOAL"
	StopWaitHumanReview      StopReason = "WAIT_HUMAN_REVIEW"
	StopNeedsHuman           StopReason = "AGENT_NEEDS_HUMAN"
	StopAgentExecutionFailed StopReason = "AGENT_EXECUTION_FAILED"
	StopVerificationRefused  StopReason = "VERIFICATION_REFUSED"
	StopVerificationInFlight StopReason = "VERIFICATION_IN_FLIGHT"
	StopScopeChanged         StopReason = "SCOPE_CHANGED"
	StopStateTampered        StopReason = "AGENT_WROTE_FORGEPILOT_STATE"
	StopRecoveryBlocked      StopReason = "RECOVERY_BLOCKED"
	StopStalled              StopReason = "STALLED"

	// These are limits this run was given.
	StopMaxSteps         StopReason = "MAX_STEPS"
	StopMaxAttempts      StopReason = "MAX_ATTEMPTS_PER_WORK"
	StopMaxDuration      StopReason = "MAX_DURATION"
	StopAgentTimeout     StopReason = "AGENT_TIMEOUT"
	StopVerifyTimeout    StopReason = "VERIFY_TIMEOUT"
	StopNoProgress       StopReason = "NO_PROGRESS"
	StopCapacityExceeded StopReason = "CAPACITY_EXCEEDED"
	StopRuntimeProtocol  StopReason = "RUNTIME_PROTOCOL_ERROR"

	// StopInterrupted is SIGINT: someone at a terminal pressed Ctrl-C.
	StopInterrupted StopReason = "INTERRUPTED"

	// StopTerminated is SIGTERM: a supervisor, a CI runner or an operator asked
	// the process to end. It stops the run exactly as an interrupt does and is
	// recorded apart from one only so the exit code can say which happened.
	StopTerminated StopReason = "TERMINATED"
)

// Exit codes. A run that reaches the Goal final-review boundary exits 0, which
// says the machine has nothing left to do — not that the Goal is finished.
const (
	ExitAwaitingReview = 0
	ExitError          = 1
	ExitNeedsHuman     = 2
	ExitLimit          = 3
	ExitInterrupted    = 130
	ExitTerminated     = 143
)

// ExitCode maps a stop reason to the documented exit code.
func (reason StopReason) ExitCode() int {
	switch reason {
	case StopAwaitingGoalReview:
		return ExitAwaitingReview
	case StopWaitGate, StopWaitGoal, StopWaitHumanReview, StopNeedsHuman,
		StopAgentExecutionFailed, StopVerificationRefused, StopVerificationInFlight,
		StopScopeChanged, StopStateTampered, StopRecoveryBlocked, StopStalled:
		return ExitNeedsHuman
	case StopMaxSteps, StopMaxAttempts, StopMaxDuration, StopAgentTimeout,
		StopVerifyTimeout, StopNoProgress, StopCapacityExceeded:
		return ExitLimit
	case StopInterrupted:
		return ExitInterrupted
	case StopTerminated:
		return ExitTerminated
	default:
		// StopRuntimeProtocol and anything added without a decision land here: an
		// unclassified stop is an execution error, never a success.
		return ExitError
	}
}
