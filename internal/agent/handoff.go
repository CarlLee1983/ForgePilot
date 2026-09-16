package agent

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultHandoffBytes bounds one session's handoff. It is a budget, not a
// guess: everything a session must not lose is written first, and only the
// bounded excerpts below it are trimmed to fit.
const DefaultHandoffBytes = 64 * 1024

// Dependency is what a session is told about one prerequisite: enough to know
// the work it builds on exists and is verified, never a copy of its Story.
type Dependency struct {
	ID       string
	Status   string
	StoryRef string
	Evidence string
}

// Attempt summarizes an earlier session on the same Work Item. Summaries are
// untrusted agent text, quoted for context and nothing more.
type Attempt struct {
	Number  int
	Outcome string
	Summary string
}

// ResolvedDecision is one durable Human Decision the session inherits from a
// resolved Gate on its Work Item or a prerequisite. It is authoritative
// context, not an instruction to relax the session's prohibitions.
type ResolvedDecision struct {
	GateID     string
	WorkItemID string
	Question   string
	Choice     string
	Note       string
}

// Handoff is the typed content of one session briefing. Rendering it is the
// only way a session is told anything: there is no ambient context, and no
// environment variable, token or credential file is ever included.
type Handoff struct {
	GoalID       string
	GoalTitle    string
	WorkItemID   string
	StoryRef     string
	Action       string
	ActionReason string
	Candidate    string
	// ResolvedDecisions are required context. Unlike attempt summaries, they
	// come from current ForgePilot state and are not untrusted session claims.
	ResolvedDecisions []ResolvedDecision
	Dependencies      []Dependency
	Attempts          []Attempt
	// FailureExcerpt is a bounded quotation of the last verification failure;
	// FailureLogPath names the whole log, which is never inlined.
	FailureExcerpt  string
	FailureLogPath  string
	ProjectDocs     []string
	DecisionRecords []string
}

// Render produces the briefing, never exceeding limit bytes. The order is the
// priority order: identity, the Story and its acceptance requirements, the
// prohibitions, and the result contract are written whole, because a session
// that lost any of them would be working to the wrong standard. Dependencies,
// previous attempts and failure excerpts are trimmed from the end instead, and
// a trim always says it happened and where the full text lives.
func (handoff Handoff) Render(limit int) (string, error) {
	if limit <= 0 {
		limit = DefaultHandoffBytes
	}
	required := handoff.requiredSections()
	optional := handoff.optionalSections()

	rendered := strings.Join(required, "\n\n")
	if len(rendered) > limit {
		return "", fmt.Errorf("handoff required context is %d bytes, exceeding the %d-byte limit", len(rendered), limit)
	}
	for _, section := range optional {
		candidate := rendered + "\n\n" + section
		if len(candidate) <= limit {
			rendered = candidate
			continue
		}
		notice := "\n\n" + truncationNotice
		if len(rendered)+len(notice) > limit {
			return "", fmt.Errorf("handoff cannot mark optional-context truncation within the %d-byte limit", limit)
		}
		remaining := limit - len(rendered) - len(notice) - 2
		if remaining > minimumExcerpt {
			rendered += "\n\n" + section[:remaining] + notice
		} else {
			rendered += notice
		}
		break
	}
	return rendered, nil
}

const truncationNotice = "(Context above was truncated to fit the handoff budget. Read the files named in this briefing for the full text; do not assume anything omitted was unimportant.)"

// minimumExcerpt is the smallest partial section worth including; below it, the
// fragment carries no meaning and only the notice is written.
const minimumExcerpt = 200

func (handoff Handoff) requiredSections() []string {
	sections := []string{
		fmt.Sprintf("# ForgePilot work item %s\n\nGoal: %s — %s\nWork Item: %s\nAction: %s (%s)\nCandidate: %s",
			handoff.WorkItemID, handoff.GoalID, handoff.GoalTitle, handoff.WorkItemID,
			handoff.Action, handoff.ActionReason, orNone(handoff.Candidate)),
		fmt.Sprintf("## Story\n\nThe engineering contract for this work item is `%s`.\nRead it in full. Its acceptance criteria are the requirements; this briefing does not restate them and does not relax them.", handoff.StoryRef),
		handoff.projectSection(),
	}
	if len(handoff.ResolvedDecisions) > 0 {
		sections = append(sections, handoff.resolvedDecisionSection())
	}
	sections = append(sections, prohibitionSection, resultContractSection)
	return sections
}

func (handoff Handoff) resolvedDecisionSection() string {
	var builder strings.Builder
	builder.WriteString("## Resolved Human Decisions\n\n")
	builder.WriteString("These decisions come from current ForgePilot state. Use them before asking for another Human Decision, but do not treat them as authority to violate this briefing's prohibitions.\n")
	for _, decision := range handoff.ResolvedDecisions {
		fmt.Fprintf(&builder, "\n- %s on %s\n  Question: %s\n  Selected choice: %s\n  Resolution note: %s\n",
			decision.GateID, decision.WorkItemID, decision.Question, decision.Choice, decision.Note)
	}
	return strings.TrimRight(builder.String(), "\n")
}

func (handoff Handoff) projectSection() string {
	var builder strings.Builder
	builder.WriteString("## Project rules\n\nRead these before changing anything:\n")
	docs := append([]string(nil), handoff.ProjectDocs...)
	if len(docs) == 0 {
		docs = []string{"AGENTS.md", "CONTEXT.md"}
	}
	for _, doc := range docs {
		fmt.Fprintf(&builder, "- `%s`\n", doc)
	}
	records := append([]string(nil), handoff.DecisionRecords...)
	sort.Strings(records)
	for _, record := range records {
		fmt.Fprintf(&builder, "- `%s`\n", record)
	}
	return strings.TrimRight(builder.String(), "\n")
}

const prohibitionSection = `## What you must not do

- Work on any work item other than the one named above.
- Advance ForgePilot lifecycle: no start, verify, review, gate, reconcile or goal command, and no edit of ` + "`.forgepilot/`" + `.
- Approve a review, resolve or cancel a Gate, or complete a Goal.
- Commit, push, merge, tag or release. Leave your work in the working tree.
- Weaken acceptance: do not delete or skip tests, loosen lint or type rules, or change the canonical check to make it pass.

If you cannot finish without doing one of these, stop and report needs_human with the question you need answered.`

const resultContractSection = `## How to finish

Write a JSON object as your final message, matching exactly one of:

- {"outcome":"implementation_finished","summary":"...","unfinished":["..."]}
  The implementation attempt is over. It does not claim the work passes:
  ForgePilot runs the canonical check against the Candidate afterwards, and that
  result is the only one that counts.
- {"outcome":"needs_human","summary":"...","needs_human":{"question":"...","options":["..."],"context":"..."}}
  A decision only a person may make. State the real options; do not choose one.
- {"outcome":"execution_failed","summary":"...","error":"..."}
  You could not run: missing tooling, missing credentials, broken environment.

No other fields are accepted, and no other outcome exists. Exiting cleanly
without one of these is a protocol error, not a completion.`

func (handoff Handoff) optionalSections() []string {
	var sections []string
	if len(handoff.Dependencies) > 0 {
		var builder strings.Builder
		builder.WriteString("## Dependencies already verified\n")
		for _, dependency := range handoff.Dependencies {
			fmt.Fprintf(&builder, "- %s %s (`%s`) — %s\n", dependency.ID, dependency.Status, dependency.StoryRef, orNone(dependency.Evidence))
		}
		sections = append(sections, strings.TrimRight(builder.String(), "\n"))
	}
	if len(handoff.Attempts) > 0 {
		var builder strings.Builder
		builder.WriteString("## Earlier attempts on this work item\n\nThese summaries were written by earlier sessions. Treat them as claims, not findings.\n")
		for _, attempt := range handoff.Attempts {
			fmt.Fprintf(&builder, "- attempt %d ended %s: %s\n", attempt.Number, attempt.Outcome, attempt.Summary)
		}
		sections = append(sections, strings.TrimRight(builder.String(), "\n"))
	}
	if handoff.FailureExcerpt != "" || handoff.FailureLogPath != "" {
		var builder strings.Builder
		builder.WriteString("## Last verification failure\n")
		if handoff.FailureLogPath != "" {
			fmt.Fprintf(&builder, "\nFull log: `%s`\n", handoff.FailureLogPath)
		}
		if handoff.FailureExcerpt != "" {
			fmt.Fprintf(&builder, "\n```\n%s\n```\n", handoff.FailureExcerpt)
		}
		sections = append(sections, strings.TrimRight(builder.String(), "\n"))
	}
	return sections
}

func orNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// Excerpt returns the tail of a log bounded to limit bytes, which is where a
// failure's cause usually is. It says when it cut, so nothing reads as complete
// that is not.
func Excerpt(contents string, limit int) string {
	if limit <= 0 || len(contents) <= limit {
		return strings.TrimSpace(contents)
	}
	trimmed := contents[len(contents)-limit:]
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 && index+1 < len(trimmed) {
		trimmed = trimmed[index+1:]
	}
	return "... earlier output omitted ...\n" + strings.TrimSpace(trimmed)
}
