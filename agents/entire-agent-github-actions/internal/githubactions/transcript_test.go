package githubactions

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestClaudeActionTranscriptSemantics(t *testing.T) {
	t.Parallel()
	agent := New()
	fixture := filepath.Join("..", "..", "testdata", "claude-action-execution.json")

	position, err := agent.GetTranscriptPosition(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if position != 6 {
		t.Fatalf("position = %d, want 6", position)
	}

	files, current, err := agent.ExtractModifiedFiles(fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"proofgate/evaluator.go", "proofgate/evaluator_test.go"}
	if !slices.Equal(files, wantFiles) || current != 6 {
		t.Fatalf("files/current = %v/%d, want %v/6", files, current, wantFiles)
	}

	prompts, err := agent.ExtractPrompts(fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantPrompts := []string{"Add the risk evaluator", "Add a regression test"}
	if !slices.Equal(prompts, wantPrompts) {
		t.Fatalf("prompts = %v, want %v", prompts, wantPrompts)
	}

	summary, ok, err := agent.ExtractSummary(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || summary != "Implemented the risk evaluator and regression coverage." {
		t.Fatalf("summary = %q/%v", summary, ok)
	}

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := agent.CalculateTokens(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 210 || usage.OutputTokens != 72 || usage.CacheCreationTokens != 20 || usage.CacheReadTokens != 18 || usage.APICallCount != 1 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestCompactTranscriptUsesEntireJSONL(t *testing.T) {
	t.Parallel()
	agent := New()
	fixture := filepath.Join("..", "..", "testdata", "claude-action-execution.json")

	result, err := agent.CompactTranscript(fixture)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	text := string(decoded)
	for _, expected := range []string{`"agent":"github-actions"`, `"type":"user"`, "Add the risk evaluator", `"name":"Write"`} {
		if !strings.Contains(text, expected) {
			t.Errorf("compact transcript does not contain %q:\n%s", expected, text)
		}
	}
}

func TestSafeFilenameRejectsTraversal(t *testing.T) {
	t.Parallel()
	for input, expected := range map[string]string{
		"../../secrets": "secrets",
		"run/42:job":    "run_42_job",
		"...":           "unknown-session",
		"run-42_1":      "run-42_1",
	} {
		if got := safeFilename(input); got != expected {
			t.Errorf("safeFilename(%q) = %q, want %q", input, got, expected)
		}
	}
}
