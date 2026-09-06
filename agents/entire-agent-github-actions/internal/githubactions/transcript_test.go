package githubactions

import (
	"encoding/base64"
	"encoding/json"
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

func TestCodexExecTranscriptSemantics(t *testing.T) {
	t.Parallel()
	agent := New()
	fixture := filepath.Join("..", "..", "testdata", "codex-exec.jsonl")

	files, current, err := agent.ExtractModifiedFiles(fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"proofgate/evaluator.go", "proofgate/evaluator_test.go"}
	if !slices.Equal(files, wantFiles) || current != 7 {
		t.Fatalf("files/current = %v/%d, want %v/7", files, current, wantFiles)
	}

	summary, ok, err := agent.ExtractSummary(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || summary != "Implemented the evaluator and regression coverage." {
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
	if usage.InputTokens != 180 || usage.CacheReadTokens != 44 || usage.CacheCreationTokens != 12 || usage.OutputTokens != 61 || usage.APICallCount != 1 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestCodexPersistedRolloutSemantics(t *testing.T) {
	t.Parallel()
	agent := New()
	fixture := filepath.Join("..", "..", "testdata", "codex-rollout.jsonl")

	files, _, err := agent.ExtractModifiedFiles(fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"proofgate/evaluator.go", "proofgate/evaluator_test.go"}
	if !slices.Equal(files, wantFiles) {
		t.Fatalf("files = %v, want %v", files, wantFiles)
	}
	prompts, err := agent.ExtractPrompts(fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prompts, []string{"Add the risk evaluator"}) {
		t.Fatalf("prompts = %v", prompts)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := agent.CalculateTokens(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 210 || usage.CacheReadTokens != 48 || usage.CacheCreationTokens != 15 || usage.OutputTokens != 72 || usage.APICallCount != 1 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if model := executionModel(data); model != "gpt-5.6-sol" {
		t.Fatalf("model = %q", model)
	}
	messages, err := parseSDKMessages(data)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(normalized), "not retained") {
		t.Fatal("normalized rollout retained patch stdout or stderr")
	}
}

func TestCursorStreamTranscriptSemantics(t *testing.T) {
	t.Parallel()
	agent := New()
	fixture := filepath.Join("..", "..", "testdata", "cursor-agent-stream.jsonl")

	files, current, err := agent.ExtractModifiedFiles(fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"proofgate/evaluator.go", "proofgate/evaluator_test.go"}
	if !slices.Equal(files, wantFiles) || current != 7 {
		t.Fatalf("files/current = %v/%d, want %v/7", files, current, wantFiles)
	}
	prompts, err := agent.ExtractPrompts(fixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prompts, []string{"Add the risk evaluator"}) {
		t.Fatalf("prompts = %v", prompts)
	}
	summary, ok, err := agent.ExtractSummary(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || summary != "Implemented the evaluator and regression coverage." {
		t.Fatalf("summary = %q/%v", summary, ok)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if model := executionModel(data); model != "GPT-5" {
		t.Fatalf("model = %q", model)
	}
}

func TestProviderDetectionAndCaptureContext(t *testing.T) {
	t.Parallel()
	for fixture, expected := range map[string]string{
		"claude-action-execution.json": "claude",
		"codex-exec.jsonl":             "codex",
		"codex-rollout.jsonl":          "codex",
		"cursor-agent-stream.jsonl":    "cursor",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		if provider := detectProvider(data); provider != expected {
			t.Errorf("detectProvider(%s) = %q, want %q", fixture, provider, expected)
		}
	}

	codex, err := os.ReadFile(filepath.Join("..", "..", "testdata", "codex-exec.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	messages, err := parseSDKMessages(codex)
	if err != nil {
		t.Fatal(err)
	}
	messages = addCaptureContext(messages, "Add the risk evaluator", "gpt-5.6-sol")
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	prompts := []string{}
	for _, message := range messages {
		if stringValue(message["type"]) == "user" {
			prompts = append(prompts, messageText(message))
		}
	}
	if !slices.Equal(prompts, []string{"Add the risk evaluator"}) || executionModel(encoded) != "gpt-5.6-sol" {
		t.Fatalf("capture context prompts/model = %v/%q", prompts, executionModel(encoded))
	}
}

func TestSafeEvidencePathRejectsOutsideWorkspace(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("ENTIRE_REPO_ROOT", repo)
	inside := filepath.Join(repo, "proofgate", "evaluator.go")
	outside := filepath.Join(filepath.Dir(repo), "secrets.env")
	if got := safeEvidencePath(inside); got != filepath.Join("proofgate", "evaluator.go") {
		t.Fatalf("inside path = %q", got)
	}
	if got := safeEvidencePath(outside); got != "" {
		t.Fatalf("outside path was retained: %q", got)
	}
	if got := safeEvidencePath("../../secrets.env"); got != "" {
		t.Fatalf("traversal path was retained: %q", got)
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
