package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/external-agents/proofgate/contracts"
	"github.com/entireio/external-agents/proofgate/engine"
	"github.com/entireio/external-agents/proofgate/warehouse"
)

func TestShouldFailUsesConfiguredThreshold(t *testing.T) {
	tests := []struct {
		threshold string
		decision  engine.Decision
		want      bool
	}{
		{"approval-required", engine.DecisionPass, false},
		{"approval-required", engine.DecisionWarn, false},
		{"approval-required", engine.DecisionApprovalRequired, true},
		{"warn", engine.DecisionWarn, true},
		{"never", engine.DecisionApprovalRequired, false},
	}
	for _, test := range tests {
		if got := shouldFail(test.threshold, test.decision); got != test.want {
			t.Fatalf("shouldFail(%q, %q) = %t, want %t", test.threshold, test.decision, got, test.want)
		}
	}
}

func TestGitHubFilesContainSafeOutputsAndReadableSummary(t *testing.T) {
	directory := t.TempDir()
	outputPath := filepath.Join(directory, "output")
	summaryPath := filepath.Join(directory, "summary")
	passport := contracts.ChangePassport{
		Change: contracts.ChangeIdentity{CheckpointID: "01CHECKPOINT"},
		Authoring: contracts.AuthoringEvidence{
			SourceAdapter: "github-actions", ProvenanceComplete: true,
		},
		Impact: contracts.ImpactEvidence{ChangedFiles: []string{"mobile/app.tsx"}, ChangedLineCount: 12},
		Tests:  contracts.TestEvidence{Total: 30},
	}
	result := engine.Result{
		Decision: engine.DecisionApprovalRequired,
		Score:    55,
		HardStops: []engine.HardStop{{
			Code: "REQUIRED_TEST_FAILED", Message: "A failing test cannot be auto-approved.",
		}},
	}
	databricks := ciDatabricksState{Mode: "roundtrip", Status: "ROUNDTRIP_COMPLETE", Warnings: []string{}}
	if err := writeGitHubOutput(outputPath, "proofgate-result.json", passport, result, "AWAITING_APPROVAL", databricks); err != nil {
		t.Fatal(err)
	}
	if err := appendGitHubSummary(summaryPath, passport, result, "AWAITING_APPROVAL", databricks); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"decision=APPROVAL_REQUIRED", "enforcement_status=AWAITING_APPROVAL", "risk_score=55", "checkpoint_id=01CHECKPOINT", "databricks_status=ROUNDTRIP_COMPLETE"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("output missing %q: %s", expected, output)
		}
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"## ProofGate: AWAITING_APPROVAL", "github-actions", "30 total / 0 failed", "REQUIRED_TEST_FAILED", "ROUNDTRIP_COMPLETE"} {
		if !strings.Contains(string(summary), expected) {
			t.Fatalf("summary missing %q: %s", expected, summary)
		}
	}
}

func TestGitHubOutputRejectsNewlineInjection(t *testing.T) {
	err := writeGitHubOutput(filepath.Join(t.TempDir(), "output"), "result.json", contracts.ChangePassport{
		Change: contracts.ChangeIdentity{CheckpointID: "safe\nunsafe=true"},
	}, engine.Result{Decision: engine.DecisionPass}, "PASS", ciDatabricksState{Status: "OFF"})
	if err == nil {
		t.Fatal("expected newline-bearing output to be rejected")
	}
}

func TestHumanReviewControlsEffectiveEnforcement(t *testing.T) {
	approved := &warehouse.ReviewDecision{Available: true, Action: "APPROVE"}
	rejected := &warehouse.ReviewDecision{Available: true, Action: "REJECT"}
	if got := enforcementStatus(engine.DecisionApprovalRequired, approved); got != "HUMAN_APPROVED" {
		t.Fatalf("approved status = %q", got)
	}
	if shouldFailEnforcement("approval-required", engine.DecisionApprovalRequired, approved) {
		t.Fatal("a recorded approval should release the gate")
	}
	if got := enforcementStatus(engine.DecisionPass, rejected); got != "HUMAN_REJECTED" {
		t.Fatalf("rejected status = %q", got)
	}
	if !shouldFailEnforcement("never", engine.DecisionPass, rejected) {
		t.Fatal("a recorded rejection must block even in observation mode")
	}
	if got := enforcementStatus(engine.DecisionApprovalRequired, nil); got != "AWAITING_APPROVAL" {
		t.Fatalf("pending status = %q", got)
	}
}

func TestDatabricksModesHaveExplicitBehavior(t *testing.T) {
	tests := []struct {
		mode          string
		needsHistory  bool
		needsExport   bool
		successStatus string
	}{
		{"off", false, false, "OFF"},
		{"history", true, false, "HISTORY_LOADED"},
		{"export", false, true, "EXPORTED"},
		{"roundtrip", true, true, "ROUNDTRIP_COMPLETE"},
	}
	for _, test := range tests {
		if modeNeedsHistory(test.mode) != test.needsHistory || modeNeedsExport(test.mode) != test.needsExport {
			t.Fatalf("unexpected behavior for mode %q", test.mode)
		}
		if got := databricksSuccessStatus(test.mode); got != test.successStatus {
			t.Fatalf("status for %q = %q, want %q", test.mode, got, test.successStatus)
		}
	}
}

func TestWriteJSONFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "result.json")
	if err := writeJSONFileAtomic(path, map[string]string{"decision": "PASS"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\n  \"decision\": \"PASS\"\n}\n" {
		t.Fatalf("unexpected result: %s", data)
	}
}
