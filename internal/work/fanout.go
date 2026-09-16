package work

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"
)

// FanoutPlan is an in-memory begin-time contract. It is deliberately not
// serializable: an interrupted operation remains an anchor-only orphan.
type FanoutPlan struct {
	AnchorWorkItemID  string
	VerificationRunID string
	Candidate         Candidate
	Runtime           map[string]string
	Optional          []FanoutMember
	Skipped           []FanoutSkip
}

// FanoutMember carries the facts completion compares before it may write an
// optional Evidence record.
type FanoutMember struct {
	WorkItemID    string
	Item          Item
	Goal          Goal
	Latest        Evidence
	Gates         []Gate
	Prerequisites []FanoutPrerequisite
}

type FanoutPrerequisite struct {
	Item   Item
	Goal   Goal
	Latest *Evidence
	Gates  []Gate
}

type FanoutSkip struct{ WorkItemID, Reason string }

// BeginVerificationFanout consumes expectedRunID and returns the same-goal,
// stale-pass cohort. The anchor remains the only item put into VERIFYING.
func (s *State) BeginVerificationFanout(anchor string, candidate Candidate, worktreePath, logPath string, runtime map[string]string, expectedRunID string, now time.Time) (FanoutPlan, error) {
	item := s.item(anchor)
	if item == nil {
		return FanoutPlan{}, fmt.Errorf("unknown work item %q", anchor)
	}
	plan := FanoutPlan{AnchorWorkItemID: anchor, VerificationRunID: expectedRunID, Candidate: candidate, Runtime: copyRuntime(runtime)}
	if err := s.BeginCandidateVerificationWithRuntimeAndRunID(anchor, candidate, worktreePath, logPath, runtime, expectedRunID, now); err != nil {
		return FanoutPlan{}, err
	}
	for _, peer := range s.WorkItems {
		if peer.ID == anchor || peer.GoalID != item.GoalID || (peer.Status != Review && peer.Status != Verified) {
			continue
		}
		latest, ok := s.LatestVerification(peer.ID)
		if !ok || latest.Result != Pass || sameCandidateContent(latest.Candidate(), candidate) {
			continue
		}
		if !s.optionalEligible(peer.ID, candidate) {
			plan.Skipped = append(plan.Skipped, FanoutSkip{peer.ID, s.fanoutIneligibleReason(peer.ID)})
			continue
		}
		plan.Optional = append(plan.Optional, s.fanoutMember(peer, latest))
	}
	closed := s.closedFanout(plan.Optional, candidate, anchor)
	selected := map[string]bool{}
	for _, member := range closed {
		selected[member.WorkItemID] = true
	}
	for _, member := range plan.Optional {
		if !selected[member.WorkItemID] {
			plan.Skipped = append(plan.Skipped, FanoutSkip{member.WorkItemID, "prerequisite closure not satisfied"})
		}
	}
	plan.Optional = closed
	return plan, nil
}

func (s *State) fanoutMember(item Item, latest Evidence) FanoutMember {
	member := FanoutMember{WorkItemID: item.ID, Item: cloneFanoutItem(item), Goal: *s.goal(item.GoalID), Latest: cloneFanoutEvidence(latest), Gates: s.fanoutGates(item.ID)}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		dependency := s.item(id)
		if dependency == nil || seen[id] {
			return
		}
		seen[id] = true
		facts := FanoutPrerequisite{Item: cloneFanoutItem(*dependency), Goal: *s.goal(dependency.GoalID), Gates: s.fanoutGates(id)}
		if latest, ok := s.LatestVerification(id); ok {
			cloned := cloneFanoutEvidence(latest)
			facts.Latest = &cloned
		}
		member.Prerequisites = append(member.Prerequisites, facts)
		for _, ancestor := range dependency.DependsOn {
			visit(ancestor)
		}
	}
	for _, dependency := range item.DependsOn {
		visit(dependency)
	}
	sort.Slice(member.Prerequisites, func(i, j int) bool {
		return workNumber(member.Prerequisites[i].Item.ID) < workNumber(member.Prerequisites[j].Item.ID)
	})
	return member
}

func cloneFanoutItem(item Item) Item {
	item.DependsOn = append([]string(nil), item.DependsOn...)
	if item.CurrentRun != nil {
		run := *item.CurrentRun
		run.Runtime = copyRuntime(run.Runtime)
		item.CurrentRun = &run
	}
	return item
}

func cloneFanoutEvidence(evidence Evidence) Evidence {
	evidence.Runtime = copyRuntime(evidence.Runtime)
	return evidence
}

func (s *State) fanoutGates(id string) []Gate {
	var gates []Gate
	for _, gate := range s.Gates {
		if gate.WorkItemID != id {
			continue
		}
		gate.Options = append([]string(nil), gate.Options...)
		if gate.DecidedAt != nil {
			decidedAt := *gate.DecidedAt
			gate.DecidedAt = &decidedAt
		}
		gates = append(gates, gate)
	}
	sort.Slice(gates, func(i, j int) bool { return gates[i].ID < gates[j].ID })
	return gates
}

func (s *State) fanoutIneligibleReason(id string) string {
	if err := s.gateBlock(id); err != nil {
		return err.Error()
	}
	item := s.item(id)
	if item == nil {
		return "work item no longer exists"
	}
	goal := s.goal(item.GoalID)
	if goal == nil || goal.Status != GoalActive {
		return "owning goal is not active"
	}
	return "work item is no longer eligible"
}

func (s *State) optionalEligible(id string, candidate Candidate) bool {
	item := s.item(id)
	if item == nil || (item.Status != Review && item.Status != Verified) || s.gateBlock(id) != nil {
		return false
	}
	goal := s.goal(item.GoalID)
	return goal != nil && goal.Status == GoalActive && s.LatestPassStale(id, candidate)
}
func (s *State) LatestPassStale(id string, candidate Candidate) bool {
	e, ok := s.LatestVerification(id)
	return ok && e.Result == Pass && !sameCandidateContent(e.Candidate(), candidate)
}

func sameCandidateContent(left, right Candidate) bool {
	if left.Kind != right.Kind {
		return false
	}
	if left.Kind == SnapshotCandidate {
		return left.Digest != "" && left.Digest == right.Digest
	}
	return left.Revision != "" && left.Revision == right.Revision
}

func (s *State) closedFanout(in []FanoutMember, candidate Candidate, anchor string) []FanoutMember {
	keep := append([]FanoutMember(nil), in...)
	for changed := true; changed; {
		changed = false
		members := map[string]bool{}
		for _, m := range keep {
			members[m.WorkItemID] = true
		}
		out := keep[:0]
		for _, m := range keep {
			item := s.item(m.WorkItemID)
			goal := s.goal(item.GoalID)
			good := true
			for _, dep := range item.DependsOn {
				d := s.item(dep)
				if d.Status == Done {
					continue
				}
				latest, pass := s.LatestVerification(dep)
				fresh := pass && latest.Result == Pass && sameCandidateContent(latest.Candidate(), candidate) && d.Status == Verified && s.gateBlock(dep) == nil
				simultaneous := goal.ReviewPolicy == ReviewPerGoal && (members[dep] || dep == anchor)
				if !fresh && !simultaneous {
					good = false
					break
				}
			}
			if good {
				out = append(out, m)
			} else {
				changed = true
			}
		}
		keep = out
	}
	sort.Slice(keep, func(i, j int) bool {
		a, b := keep[i].Item, keep[j].Item
		if a.CreatedAt.Equal(b.CreatedAt) {
			return workNumber(a.ID) < workNumber(b.ID)
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return keep
}

// RecordVerificationFanoutPass settles the anchor and unchanged surviving
// optional members as one in-memory transition. Storage.Update supplies the
// transaction boundary around it.
func (s *State) RecordVerificationFanoutPass(plan FanoutPlan, command string, repository *RepositoryState, now time.Time) ([]Evidence, []FanoutSkip, error) {
	anchor := s.item(plan.AnchorWorkItemID)
	if anchor == nil || anchor.Status != Verifying || anchor.CurrentRun == nil || anchor.CurrentRun.VerificationRunID != plan.VerificationRunID || anchor.CurrentRun.Candidate() != plan.Candidate {
		return nil, nil, errors.New("verification state changed during check")
	}
	survivors := make([]FanoutMember, 0, len(plan.Optional))
	skips := append([]FanoutSkip(nil), plan.Skipped...)
	for _, m := range plan.Optional {
		item := s.item(m.WorkItemID)
		latest, ok := s.LatestVerification(m.WorkItemID)
		if item == nil || !ok || latest.ID != m.Latest.ID || !s.optionalEligible(m.WorkItemID, plan.Candidate) || !sameFanoutMember(s.fanoutMember(*item, latest), m) {
			skips = append(skips, FanoutSkip{m.WorkItemID, "changed or no longer eligible"})
			continue
		}
		survivors = append(survivors, m)
	}
	closed := s.closedFanout(survivors, plan.Candidate, plan.AnchorWorkItemID)
	present := map[string]bool{}
	for _, m := range closed {
		present[m.WorkItemID] = true
	}
	for _, m := range survivors {
		if !present[m.WorkItemID] {
			skips = append(skips, FanoutSkip{m.WorkItemID, "prerequisite closure no longer satisfied"})
		}
	}
	// All checks precede the first mutation.
	ids := append([]string{anchor.ID}, func() []string {
		x := []string{}
		for _, m := range closed {
			x = append(x, m.WorkItemID)
		}
		return x
	}()...)
	for _, id := range ids {
		if s.item(id) == nil {
			return nil, nil, fmt.Errorf("unknown work item %q", id)
		}
	}
	evidence := make([]Evidence, 0, len(ids))
	for _, id := range ids {
		item := s.item(id)
		goal := s.goal(item.GoalID)
		e := Evidence{ID: s.takeEvidenceID(), Type: VerificationEvidence, Repository: goal.Repository, WorkItemID: id, StoryRef: item.StoryRef, Revision: plan.Candidate.Revision, CandidateKind: plan.Candidate.Kind, BaseRevision: plan.Candidate.BaseRevision, CandidateDigest: plan.Candidate.Digest, Command: command, ExitCode: intPtr(0), Result: Pass, Runtime: copyRuntime(plan.Runtime), VerificationRunID: plan.VerificationRunID, CreatedAt: now}
		evidence = append(evidence, e)
	}
	s.Evidence = append(s.Evidence, evidence...)
	anchor.CurrentRun = nil
	for _, id := range ids {
		item := s.item(id)
		if s.goal(item.GoalID).ReviewPolicy == ReviewPerGoal {
			item.Status = Verified
		} else {
			item.Status = Running
		}
		item.UpdatedAt = now
	}
	s.refreshReady(repository, now)
	return evidence, skips, nil
}
func intPtr(v int) *int { return &v }
func sameFanoutMember(a, b FanoutMember) bool {
	return reflect.DeepEqual(a, b)
}
