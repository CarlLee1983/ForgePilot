// Command export-plan is the one-time bridge from the pre-schema-19 ForgePilot
// state to Goal Plans. It reads an old .forgepilot/state.json and writes one
// Goal Plan per Goal that still has unfinished work, ready for
// `forgepilot goal import`.
//
// It is deliberately not part of the product binary and does not import
// internal/work: it carries its own minimal, non-strict description of the old
// state, so it keeps compiling after the old model is gone from the product.
// Delete it once every repository has been migrated.
//
//	go run ./tools/export-plan --state <old state.json> --out <dir>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// currentSchema is the schema `forgepilot goal import` writes. State at or
// above it has nothing to export.
const currentSchema = 19

// Only the fields the export needs. Decoding is non-strict on purpose: every
// other field of every old schema version is ignored.
type oldState struct {
	SchemaVersion int       `json:"schema_version"`
	Goals         []oldGoal `json:"goals"`
	WorkItems     []oldItem `json:"work_items"`
}

type oldGoal struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Status       string `json:"status"`
	ReviewPolicy string `json:"review_policy"`
}

type oldItem struct {
	ID        string   `json:"id"`
	GoalID    string   `json:"goal_id"`
	StoryRef  string   `json:"story_ref"`
	Status    string   `json:"status"`
	DependsOn []string `json:"depends_on"`
}

// The Goal Plan shape `goal import` reads.
type plan struct {
	Goal  planGoal `json:"goal"`
	Nodes []node   `json:"nodes"`
}

type planGoal struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	RequireApproval bool   `json:"require_approval"`
}

type node struct {
	ID        string   `json:"id"`
	Story     string   `json:"story"`
	DependsOn []string `json:"depends_on"`
}

// exported is one Goal's plan plus what the operator should know about it.
type exported struct {
	Plan  plan
	Notes []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("export-plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	statePath := flags.String("state", "", "path to the old .forgepilot/state.json (required)")
	outDir := flags.String("out", "", "directory to write one <goal-id>.json Goal Plan per Goal into (required)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *statePath == "" || *outDir == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: export-plan --state <old state.json> --out <dir>")
		return 2
	}
	if err := export(*statePath, *outDir, stdout); err != nil {
		fmt.Fprintf(stderr, "export-plan: %v\n", err)
		return 1
	}
	return 0
}

func export(statePath, outDir string, report io.Writer) error {
	contents, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state oldState
	if err := json.Unmarshal(contents, &state); err != nil {
		return fmt.Errorf("read %s: %w", statePath, err)
	}
	if state.SchemaVersion < 1 {
		return fmt.Errorf("%s has no usable schema_version; is it a ForgePilot state.json?", statePath)
	}
	if state.SchemaVersion >= currentSchema {
		return fmt.Errorf("%s is schema %d, which this ForgePilot reads directly; there is nothing to export", statePath, state.SchemaVersion)
	}

	plans, skipped, problems := buildPlans(state)
	if len(problems) > 0 {
		return fmt.Errorf("nothing was written:\n  %s", strings.Join(problems, "\n  "))
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	for _, item := range plans {
		path := filepath.Join(outDir, item.Plan.Goal.ID+".json")
		encoded, err := json.MarshalIndent(item.Plan, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0644); err != nil {
			return err
		}
		fmt.Fprintf(report, "%s: %d nodes\n", path, len(item.Plan.Nodes))
		for _, note := range item.Notes {
			fmt.Fprintf(report, "  note: %s\n", note)
		}
	}
	for _, note := range skipped {
		fmt.Fprintln(report, note)
	}
	if len(plans) == 0 {
		fmt.Fprintln(report, "no ACTIVE or BLOCKED Goal has unfinished work; nothing to export")
	}
	return nil
}

// buildPlans turns every ACTIVE or BLOCKED Goal that still has unfinished work
// into a plan. Problems are collected for every Goal rather than stopping at the
// first, so one run tells the operator everything to fix.
func buildPlans(state oldState) (plans []exported, skipped, problems []string) {
	finished := map[string]bool{}
	for _, item := range state.WorkItems {
		finished[item.ID] = item.Status == "DONE" || item.Status == "VERIFIED"
	}
	for _, goal := range state.Goals {
		if goal.Status != "ACTIVE" && goal.Status != "BLOCKED" {
			continue
		}
		result := exported{Plan: plan{Goal: planGoal{
			ID: goal.ID, Title: goal.Title, Description: goal.Description,
			// Only an explicit GOAL policy skips per-item review; WORK_ITEM and the
			// absence of a policy (state from before policies existed) both approve.
			RequireApproval: goal.ReviewPolicy != "GOAL",
		}}}
		if !validID(goal.ID) {
			problems = append(problems, fmt.Sprintf("goal %q: id is not a valid Goal Plan ID (letters, digits, '.', '_', '-', at most 64, starting with a letter or digit)", goal.ID))
			continue
		}
		restarted, dropped := 0, 0
		for _, item := range state.WorkItems {
			if item.GoalID != goal.ID {
				continue
			}
			if finished[item.ID] {
				dropped++
				continue
			}
			if !validID(item.ID) {
				problems = append(problems, fmt.Sprintf("goal %q: work item %q: id is not a valid node ID", goal.ID, item.ID))
				continue
			}
			if item.Status == "RUNNING" || item.Status == "VERIFYING" || item.Status == "REVIEW" {
				restarted++
			}
			dependsOn := []string{}
			for _, dependency := range item.DependsOn {
				if !finished[dependency] {
					dependsOn = append(dependsOn, dependency)
				}
			}
			result.Plan.Nodes = append(result.Plan.Nodes, node{ID: item.ID, Story: item.StoryRef, DependsOn: dependsOn})
		}
		if len(result.Plan.Nodes) == 0 {
			// Not an error: a Goal whose work is all finished has nothing to carry over.
			skipped = append(skipped, fmt.Sprintf("goal %s: no unfinished work; skipped", goal.ID))
			continue
		}
		if dropped > 0 {
			result.Notes = append(result.Notes, fmt.Sprintf("%d finished (DONE or VERIFIED) work items were left out and their dependency edges removed", dropped))
		}
		if restarted > 0 {
			result.Notes = append(result.Notes, fmt.Sprintf("%d work items were RUNNING, VERIFYING or in REVIEW; they restart as new nodes with no history", restarted))
		}
		if goal.Status == "BLOCKED" {
			result.Notes = append(result.Notes, "the Goal was BLOCKED; imported Goals are always ACTIVE, so open a Gate on the work that should wait")
		}
		plans = append(plans, result)
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].Plan.Goal.ID < plans[j].Plan.Goal.ID })
	return plans, skipped, problems
}

// validID is the Goal Plan ID rule, restated here because the tool must not
// import the product's packages.
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alphanumeric := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alphanumeric && (i == 0 || (c != '.' && c != '_' && c != '-')) {
			return false
		}
	}
	return true
}
