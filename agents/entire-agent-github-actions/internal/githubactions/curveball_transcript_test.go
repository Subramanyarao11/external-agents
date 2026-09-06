package githubactions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/protocol"
)

const (
	v2SessionID = "btw-track3-demo-001"
	v2Prompt    = "Add coupon validation to checkout. Coupons should be rejected if expired, disabled, or below the minimum cart value. Add tests."
	v2Summary   = "Coupon validation implemented and tested."
	v2Model     = "acmecode-pro"
)

// The organizer fixture is preserved exactly except for the instructed
// mechanical restoration of chat-rendered \_ sequences to ordinary underscores
// and the three explicitly supplied timestamp corrections.

var v2ModifiedFiles = []string{
	"src/checkout/apply_coupon.ts",
	"tests/checkout/apply_coupon.test.ts",
}

func TestCurveballTranscriptFormats(t *testing.T) {
	t.Parallel()

	t.Run("legacy JSON array", func(t *testing.T) {
		t.Parallel()
		assertTranscriptSemantics(t, filepath.Join("..", "..", "testdata", "claude-action-execution.json"), transcriptExpectations{
			position: 6,
			files: []string{
				"proofgate/evaluator.go",
				"proofgate/evaluator_test.go",
			},
			prompts: []string{"Add the risk evaluator", "Add a regression test"},
			summary: "Implemented the risk evaluator and regression coverage.",
			usage: protocol.TokenUsageResponse{
				InputTokens:         210,
				CacheCreationTokens: 20,
				CacheReadTokens:     18,
				OutputTokens:        72,
				APICallCount:        1,
			},
			compactContains: []string{`"agent":"github-actions"`, `"name":"Write"`},
		})
	})

	t.Run("official JSONL", func(t *testing.T) {
		t.Parallel()
		assertV2TranscriptSemantics(t, v2FixturePath())
	})
}

func TestCurveballUnknownEventIgnored(t *testing.T) {
	t.Parallel()
	data := readFixture(t, v2FixturePath())
	firstLineEnd := bytes.IndexByte(data, '\n') + 1
	unknown := []byte(`{"timestamp":"2026-09-06T09:00:01.000+05:30","event":"future_signal","session_id":"btw-track3-demo-001","payload":{"preserve":"known records"}}` + "\n")
	withUnknown := append(append(append([]byte{}, data[:firstLineEnd]...), unknown...), data[firstLineEnd:]...)

	path := filepath.Join(t.TempDir(), "with-unknown.jsonl")
	if err := os.WriteFile(path, withUnknown, 0o600); err != nil {
		t.Fatal(err)
	}
	assertV2TranscriptSemantics(t, path)
}

func TestCurveballIncompleteJSONLReturnsPartialResult(t *testing.T) {
	t.Parallel()
	data := bytes.TrimSuffix(readFixture(t, v2FixturePath()), []byte("\n"))
	lastLine := bytes.LastIndexByte(data, '\n') + 1
	incomplete := append(append([]byte{}, data[:lastLine]...), []byte(`{"timestamp":"2026-09-06T09:05`)...)

	path := filepath.Join(t.TempDir(), "incomplete.jsonl")
	if err := os.WriteFile(path, incomplete, 0o600); err != nil {
		t.Fatal(err)
	}
	assertTranscriptSemantics(t, path, transcriptExpectations{
		position: 16,
		files:    v2ModifiedFiles,
		prompts:  []string{v2Prompt},
		summary:  v2Summary,
		model:    v2Model,
		usage: protocol.TokenUsageResponse{
			InputTokens:  8421,
			OutputTokens: 2194,
			APICallCount: 1,
		},
		compactContains: []string{`"agent":"github-actions"`, v2Prompt, `"name":"file_changed"`, "src/checkout/apply_coupon.ts"},
	})
}

func TestCurveballMalformedCompleteJSONLRecordFails(t *testing.T) {
	t.Parallel()
	data := readFixture(t, v2FixturePath())
	firstLineEnd := bytes.IndexByte(data, '\n') + 1
	malformed := append(append(append([]byte{}, data[:firstLineEnd]...), []byte("{\"event\":\n")...), data[firstLineEnd:]...)

	if _, err := parseSDKMessages(malformed); err == nil {
		t.Fatal("parseSDKMessages accepted a malformed complete JSONL record")
	}
}

func TestCurveballCaptureFinishPreservesRawTranscriptAndLifecycle(t *testing.T) {
	repo := t.TempDir()
	runnerTemp := t.TempDir()
	t.Setenv("ENTIRE_REPO_ROOT", repo)
	t.Setenv("RUNNER_TEMP", runnerTemp)
	logPath := installFakeEntire(t)
	agent := New()

	start, err := agent.CaptureStart(v2SessionID, v2Prompt)
	if err != nil {
		t.Fatal(err)
	}
	finish, err := agent.CaptureFinish(v2FixturePath(), v2SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if start.SessionRef != finish.SessionRef || finish.SessionID != v2SessionID {
		t.Fatalf("session identity changed: start=%+v finish=%+v", start, finish)
	}
	if !slices.Equal(finish.ModifiedFiles, v2ModifiedFiles) || !finish.HasSummary {
		t.Fatalf("unexpected capture result: %+v", finish)
	}
	if finish.TokenUsage.InputTokens != 8421 || finish.TokenUsage.OutputTokens != 2194 || finish.TokenUsage.APICallCount != 1 {
		t.Fatalf("unexpected capture token usage: %+v", finish.TokenUsage)
	}
	stored, err := os.ReadFile(finish.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	if fixture := readFixture(t, v2FixturePath()); !bytes.Equal(stored, fixture) {
		t.Fatal("CaptureFinish did not preserve the official JSONL bytes exactly")
	}

	assertLifecycleLog(t, logPath, agent)
}

func TestCurveballCaptureFinishPreservesIncompleteTranscript(t *testing.T) {
	repo := t.TempDir()
	runnerTemp := t.TempDir()
	t.Setenv("ENTIRE_REPO_ROOT", repo)
	t.Setenv("RUNNER_TEMP", runnerTemp)
	_ = installFakeEntire(t)

	data := bytes.TrimSuffix(readFixture(t, v2FixturePath()), []byte("\n"))
	lastLine := bytes.LastIndexByte(data, '\n') + 1
	incomplete := append(append([]byte{}, data[:lastLine]...), []byte(`{"event":"session_ended"`)...)
	path := filepath.Join(t.TempDir(), "incomplete-capture.jsonl")
	if err := os.WriteFile(path, incomplete, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := New().CaptureFinish(path, v2SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.ModifiedFiles, v2ModifiedFiles) || !result.HasSummary || result.TokenUsage.InputTokens != 8421 {
		t.Fatalf("partial capture discarded complete evidence: %+v", result)
	}
	stored, err := os.ReadFile(result.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, incomplete) {
		t.Fatal("partial capture was corrupted while being stored")
	}
}

type transcriptExpectations struct {
	position        int
	files           []string
	prompts         []string
	summary         string
	model           string
	usage           protocol.TokenUsageResponse
	compactContains []string
}

func assertV2TranscriptSemantics(t *testing.T, path string) {
	t.Helper()
	assertTranscriptSemantics(t, path, transcriptExpectations{
		position: 17,
		files:    v2ModifiedFiles,
		prompts:  []string{v2Prompt},
		summary:  v2Summary,
		model:    v2Model,
		usage: protocol.TokenUsageResponse{
			InputTokens:  8421,
			OutputTokens: 2194,
			APICallCount: 1,
		},
		compactContains: []string{`"agent":"github-actions"`, v2Prompt, `"name":"file_changed"`, "src/checkout/apply_coupon.ts"},
	})
}

func assertTranscriptSemantics(t *testing.T, path string, want transcriptExpectations) {
	t.Helper()
	agent := New()
	position, err := agent.GetTranscriptPosition(path)
	if err != nil {
		t.Fatal(err)
	}
	if position != want.position {
		t.Errorf("position = %d, want %d", position, want.position)
	}
	files, current, err := agent.ExtractModifiedFiles(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, want.files) || current != want.position {
		t.Errorf("files/current = %v/%d, want %v/%d", files, current, want.files, want.position)
	}
	prompts, err := agent.ExtractPrompts(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prompts, want.prompts) {
		t.Errorf("prompts = %v, want %v", prompts, want.prompts)
	}
	summary, ok, err := agent.ExtractSummary(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || summary != want.summary {
		t.Errorf("summary = %q/%v, want %q/true", summary, ok, want.summary)
	}
	data := readFixture(t, path)
	usage, err := agent.CalculateTokens(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if usage != want.usage {
		t.Errorf("usage = %+v, want %+v", usage, want.usage)
	}
	if want.model != "" && executionModel(data) != want.model {
		t.Errorf("model = %q, want %q", executionModel(data), want.model)
	}
	compact, err := agent.CompactTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(compact.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(decoded), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("compact transcript contains invalid JSONL %q: %v", line, err)
		}
	}
	for _, expected := range want.compactContains {
		if !bytes.Contains(decoded, []byte(expected)) {
			t.Errorf("compact transcript does not contain %q:\n%s", expected, decoded)
		}
	}
}

func v2FixturePath() string {
	return filepath.Join("..", "..", "testdata", "claude-action-execution-v2.jsonl")
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func installFakeEntire(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "hooks.log")
	binary := filepath.Join(dir, "entire")
	script := "#!/bin/sh\npayload=$(cat)\nprintf '%s\\t%s\\n' \"$*\" \"$payload\" >> \"$ENTIRE_CAPTURE_LOG\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENTIRE_CLI_PATH", binary)
	t.Setenv("ENTIRE_CAPTURE_LOG", logPath)
	return logPath
}

func assertLifecycleLog(t *testing.T, logPath string, agent *Agent) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	wantHooks := []string{HookRunStart, HookTurnStart, HookTurnEnd, HookRunEnd}
	wantTypes := []int{1, 2, 3, 5}
	if len(lines) != len(wantHooks) {
		t.Fatalf("lifecycle calls = %d, want %d:\n%s", len(lines), len(wantHooks), data)
	}
	for i, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		wantArgs := "hooks github-actions " + wantHooks[i]
		if len(parts) != 2 || parts[0] != wantArgs {
			t.Fatalf("lifecycle call %d = %q, want args %q and payload", i, line, wantArgs)
		}
		event, err := agent.ParseHook(wantHooks[i], []byte(parts[1]))
		if err != nil {
			t.Fatal(err)
		}
		if event == nil || event.Type != wantTypes[i] || event.SessionID != v2SessionID {
			t.Fatalf("lifecycle event %d = %+v, want type %d/session %s", i, event, wantTypes[i], v2SessionID)
		}
		if wantHooks[i] == HookTurnStart && event.Prompt != v2Prompt {
			t.Errorf("turn-start prompt = %q, want %q", event.Prompt, v2Prompt)
		}
		if wantHooks[i] == HookTurnEnd && (event.ResponseMessage != v2Summary || event.Model != v2Model) {
			t.Errorf("turn-end response/model = %q/%q, want %q/%q", event.ResponseMessage, event.Model, v2Summary, v2Model)
		}
	}
}
