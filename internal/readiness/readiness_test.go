package readiness

import (
	"strings"
	"testing"
	"time"
)

func TestReviewReportsRawByteSourceDigestMismatch(t *testing.T) {
	story := []byte("# Story\n")
	acceptance := []byte("# Acceptance\n")
	contract := Contract{
		SchemaVersion:      1,
		StoryRef:           "specs/stories/example",
		StoryMDDigest:      Digest(story),
		AcceptanceMDDigest: Digest(acceptance),
	}

	report := Review(Input{Items: []Item{{
		ID: "WI-001", StoryRef: contract.StoryRef, Contract: &contract,
		StoryMD:      append([]byte(nil), story...),
		AcceptanceMD: []byte("# Acceptance\r\n"),
	}}})

	if len(report.Defects) != 1 {
		t.Fatalf("defects = %#v, want one source digest mismatch", report.Defects)
	}
	defect := report.Defects[0]
	if defect.Code != SourceDigestMismatch || defect.SourceFile != "acceptance.md" {
		t.Fatalf("defect = %#v, want acceptance.md source digest mismatch", defect)
	}
	if defect.DeclaredDigest != Digest(acceptance) || defect.ObservedDigest != Digest([]byte("# Acceptance\r\n")) {
		t.Fatalf("digest report = %#v", defect)
	}
}

func TestReviewOrdersFindingsByWorkItemCreationTime(t *testing.T) {
	earlier := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	report := Review(Input{Items: []Item{
		{ID: "WI-002", StoryRef: "specs/stories/later", CreatedAt: earlier.Add(time.Second), PreflightDefects: []Defect{{Code: MissingContract, WorkItemID: "WI-002", StoryRef: "specs/stories/later"}}},
		{ID: "WI-001", StoryRef: "specs/stories/earlier", CreatedAt: earlier, PreflightDefects: []Defect{{Code: MissingContract, WorkItemID: "WI-001", StoryRef: "specs/stories/earlier"}}},
	}})
	if len(report.Defects) != 2 || report.Defects[0].WorkItemID != "WI-001" {
		t.Fatalf("findings = %#v, want earlier Work Item first", report.Defects)
	}
}

func TestDecodeRejectsUnknownContractFields(t *testing.T) {
	_, err := Decode([]byte(`{
  "schema_version": 1,
  "story_ref": "specs/stories/example",
  "story_md_digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "acceptance_md_digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "not_in_v1": true
}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Decode error = %v, want unknown-field rejection", err)
	}
}

func TestReviewRejectsRunnerWorkerPushWithoutInterpretingMarkdown(t *testing.T) {
	story, acceptance := []byte("unparsed story"), []byte("unparsed acceptance")
	contract := Contract{SchemaVersion: 1, StoryRef: "specs/stories/example", StoryMDDigest: Digest(story), AcceptanceMDDigest: Digest(acceptance),
		Criteria: []Criterion{{ID: "AC-001", Owner: "runner_worker", Operations: []string{"plan", "push"}}}}
	report := Review(Input{Items: []Item{{ID: "WI-001", StoryRef: contract.StoryRef, Contract: &contract, StoryMD: story, AcceptanceMD: acceptance}}})
	if len(report.Defects) != 1 || report.Defects[0].Code != OutOfScopeOperation {
		t.Fatalf("report = %#v, want runner-worker push defect", report)
	}
}

func TestReviewRequiresTransitiveProducerAndDeclaredDecisionFollowup(t *testing.T) {
	contract := func(ref string) *Contract {
		story, acceptance := []byte(ref+" story"), []byte(ref+" acceptance")
		return &Contract{SchemaVersion: 1, StoryRef: ref, StoryMDDigest: Digest(story), AcceptanceMDDigest: Digest(acceptance)}
	}
	producer := contract("specs/stories/producer")
	producer.Outputs = []OutputDeclaration{{ID: "artifact-a"}}
	consumer := contract("specs/stories/consumer")
	consumer.Inputs = []InputDeclaration{{ID: "input-a", Source: InputSource{PrerequisiteOutput: &PrerequisiteOutput{OutputID: "artifact-a"}}}}
	consumer.DecisionFollowUps = []DecisionFollowUp{{GateID: "GATE-001", Choice: "ship", FollowUpStoryRef: "specs/stories/followup"}}
	items := []Item{
		{ID: "WI-001", StoryRef: producer.StoryRef, Contract: producer, StoryMD: []byte(producer.StoryRef + " story"), AcceptanceMD: []byte(producer.StoryRef + " acceptance")},
		{ID: "WI-002", StoryRef: consumer.StoryRef, Contract: consumer, StoryMD: []byte(consumer.StoryRef + " story"), AcceptanceMD: []byte(consumer.StoryRef + " acceptance")},
	}
	report := Review(Input{Items: items, Gates: []Gate{{ID: "GATE-001", Choice: "ship", Resolved: true}}})
	if len(report.Defects) != 2 || report.Defects[0].Code != UndeliveredPrerequisiteInput || report.Defects[1].Code != UnplannedDecisionFollowup {
		t.Fatalf("report = %#v", report)
	}
}
