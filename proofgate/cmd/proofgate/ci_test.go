package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/external-agents/proofgate/contracts"
	"github.com/entireio/external-agents/proofgate/engine"
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
	if err := writeGitHubOutput(outputPath, "proofgate-result.json", passport, result); err != nil {
		t.Fatal(err)
	}
	if err := appendGitHubSummary(summaryPath, passport, result); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"decision=APPROVAL_REQUIRED", "risk_score=55", "checkpoint_id=01CHECKPOINT"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("output missing %q: %s", expected, output)
		}
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"## ProofGate: APPROVAL_REQUIRED", "github-actions", "30 total / 0 failed", "REQUIRED_TEST_FAILED"} {
		if !strings.Contains(string(summary), expected) {
			t.Fatalf("summary missing %q: %s", expected, summary)
		}
	}
}

func TestGitHubOutputRejectsNewlineInjection(t *testing.T) {
	err := writeGitHubOutput(filepath.Join(t.TempDir(), "output"), "result.json", contracts.ChangePassport{
		Change: contracts.ChangeIdentity{CheckpointID: "safe\nunsafe=true"},
	}, engine.Result{Decision: engine.DecisionPass})
	if err == nil {
		t.Fatal("expected newline-bearing output to be rejected")
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
