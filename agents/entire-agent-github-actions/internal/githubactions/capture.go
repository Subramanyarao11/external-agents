package githubactions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/protocol"
)

const maxExecutionFileBytes = 128 << 20

type CaptureResult struct {
	SessionID       string                      `json:"session_id"`
	SessionRef      string                      `json:"session_ref"`
	Provider        string                      `json:"provider"`
	Model           string                      `json:"model,omitempty"`
	ModifiedFiles   []string                    `json:"modified_files"`
	HasSummary      bool                        `json:"has_summary"`
	TokenUsage      protocol.TokenUsageResponse `json:"token_usage"`
	LifecycleEvents int                         `json:"lifecycle_events"`
}

func (a *Agent) CaptureStart(sessionID, prompt, provider, model string) (CaptureResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return CaptureResult{}, errors.New("session-id is required")
	}
	sessionRef, err := a.sessionRef(sessionID)
	if err != nil {
		return CaptureResult{}, err
	}

	provider, err = normalizeProvider(provider)
	if err != nil {
		return CaptureResult{}, err
	}
	metadata := mergeMetadata(githubMetadata(), map[string]interface{}{"provider": provider, "model": strings.TrimSpace(model)})

	if err := a.dispatchLifecycle(HookRunStart, protocol.HookInputJSON{
		HookType:   HookRunStart,
		SessionID:  sessionID,
		SessionRef: sessionRef,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		RawData:    metadata,
	}); err != nil {
		return CaptureResult{}, err
	}
	if err := a.dispatchLifecycle(HookTurnStart, protocol.HookInputJSON{
		HookType:   HookTurnStart,
		SessionID:  sessionID,
		SessionRef: sessionRef,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		UserPrompt: prompt,
		RawData:    metadata,
	}); err != nil {
		return CaptureResult{}, err
	}

	return CaptureResult{
		SessionID:       sessionID,
		SessionRef:      sessionRef,
		Provider:        provider,
		Model:           strings.TrimSpace(model),
		ModifiedFiles:   []string{},
		LifecycleEvents: 2,
	}, nil
}

func (a *Agent) CaptureFinish(executionFile, sessionID, provider, prompt, model string) (CaptureResult, error) {
	executionFile = strings.TrimSpace(executionFile)
	sessionID = strings.TrimSpace(sessionID)
	if executionFile == "" {
		return CaptureResult{}, errors.New("execution-file is required")
	}
	if sessionID == "" {
		return CaptureResult{}, errors.New("session-id is required")
	}
	provider, err := normalizeProvider(provider)
	if err != nil {
		return CaptureResult{}, err
	}
	info, err := os.Stat(executionFile)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("inspect execution file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return CaptureResult{}, errors.New("execution-file must be a regular file")
	}
	if info.Size() > maxExecutionFileBytes {
		return CaptureResult{}, fmt.Errorf("execution-file exceeds %d bytes", maxExecutionFileBytes)
	}
	data, err := os.ReadFile(executionFile)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("read execution file: %w", err)
	}
	messages, err := parseSDKMessages(data)
	if err != nil {
		return CaptureResult{}, err
	}
	detectedProvider := detectProvider(data)
	if detectedProvider == "unknown" {
		return CaptureResult{}, errors.New("execution file is not a recognized Claude, Codex, or Cursor transcript")
	}
	if provider == "auto" {
		provider = detectedProvider
	} else if provider != detectedProvider {
		return CaptureResult{}, fmt.Errorf("provider %q does not match detected %q transcript", provider, detectedProvider)
	}
	if len(messages) == 0 {
		return CaptureResult{}, errors.New("execution transcript contains no usable records")
	}
	messages = addCaptureContext(messages, prompt, model)
	data, err = json.Marshal(messages)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("encode normalized execution transcript: %w", err)
	}

	sessionRef, err := a.sessionRef(sessionID)
	if err != nil {
		return CaptureResult{}, err
	}
	if err := a.WriteSession(protocol.AgentSessionJSON{
		SessionID:  sessionID,
		AgentName:  agentName,
		RepoPath:   protocol.RepoRoot(),
		SessionRef: sessionRef,
		NativeData: data,
	}); err != nil {
		return CaptureResult{}, err
	}

	files, _, err := a.ExtractModifiedFiles(sessionRef, 0)
	if err != nil {
		return CaptureResult{}, err
	}
	summary, hasSummary, err := a.ExtractSummary(sessionRef)
	if err != nil {
		return CaptureResult{}, err
	}
	usage, err := a.CalculateTokens(data, 0)
	if err != nil {
		return CaptureResult{}, err
	}
	metadata := mergeMetadata(githubMetadata(), map[string]interface{}{"provider": provider})
	resolvedModel := strings.TrimSpace(model)
	if resolvedModel == "" {
		resolvedModel = executionModel(data)
	}
	if resolvedModel != "" {
		metadata["model"] = resolvedModel
	}

	if err := a.dispatchLifecycle(HookTurnEnd, protocol.HookInputJSON{
		HookType:   HookTurnEnd,
		SessionID:  sessionID,
		SessionRef: sessionRef,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		RawData:    mergeMetadata(metadata, map[string]interface{}{"response_message": summary}),
	}); err != nil {
		return CaptureResult{}, err
	}
	if err := a.dispatchLifecycle(HookRunEnd, protocol.HookInputJSON{
		HookType:   HookRunEnd,
		SessionID:  sessionID,
		SessionRef: sessionRef,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		RawData:    metadata,
	}); err != nil {
		return CaptureResult{}, err
	}

	return CaptureResult{
		SessionID:       sessionID,
		SessionRef:      sessionRef,
		Provider:        provider,
		Model:           resolvedModel,
		ModifiedFiles:   files,
		HasSummary:      hasSummary,
		TokenUsage:      usage,
		LifecycleEvents: 2,
	}, nil
}

func normalizeProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "auto"
	}
	switch provider {
	case "auto", "claude", "codex", "cursor":
		return provider, nil
	default:
		return "", fmt.Errorf("unsupported provider %q; use auto, claude, codex, or cursor", provider)
	}
}

func detectProvider(data []byte) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return "claude"
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	for {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			break
		}
		switch stringValue(record["type"]) {
		case "thread.started", "turn.started", "item.started", "item.completed", "turn.completed", "session_meta", "turn_context", "response_item", "event_msg":
			return "codex"
		case "tool_call":
			return "cursor"
		case "system":
			if stringValue(record["apiKeySource"]) != "" {
				return "cursor"
			}
		}
	}
	return "unknown"
}

func addCaptureContext(messages []sdkMessage, prompt, model string) []sdkMessage {
	prompt = strings.TrimSpace(prompt)
	model = strings.TrimSpace(model)
	result := append([]sdkMessage(nil), messages...)

	if model != "" {
		updated := false
		for _, message := range result {
			if stringValue(message["type"]) == "system" {
				if stringValue(message["model"]) == "" {
					message["model"] = model
				}
				updated = true
				break
			}
		}
		if !updated {
			result = append([]sdkMessage{{"type": "system", "subtype": "init", "model": model}}, result...)
		}
	}

	if prompt != "" {
		hasPrompt := false
		for _, message := range result {
			if stringValue(message["type"]) == "user" && messageText(message) == prompt {
				hasPrompt = true
				break
			}
		}
		if !hasPrompt {
			index := 0
			if len(result) > 0 && stringValue(result[0]["type"]) == "system" {
				index = 1
			}
			user := sdkMessage{"type": "user", "content": []any{map[string]any{"type": "text", "text": prompt}}}
			result = append(result, nil)
			copy(result[index+1:], result[index:])
			result[index] = user
		}
	}
	return result
}

func (a *Agent) sessionRef(sessionID string) (string, error) {
	dir, err := a.GetSessionDir(protocol.RepoRoot())
	if err != nil {
		return "", err
	}
	return a.ResolveSessionFile(dir, sessionID), nil
}

func (a *Agent) dispatchLifecycle(hook string, input protocol.HookInputJSON) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	binary := strings.TrimSpace(os.Getenv("ENTIRE_CLI_PATH"))
	if binary == "" {
		binary = "entire"
	}
	cmd := exec.Command(binary, "hooks", agentName, hook)
	cmd.Dir = protocol.RepoRoot()
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("Entire %s hook failed: %w: %s", hook, err, message)
		}
		return fmt.Errorf("Entire %s hook failed: %w", hook, err)
	}
	return nil
}

func githubMetadata() map[string]interface{} {
	metadata := map[string]interface{}{}
	for key, envName := range map[string]string{
		"repository":  "GITHUB_REPOSITORY",
		"run_id":      "GITHUB_RUN_ID",
		"run_attempt": "GITHUB_RUN_ATTEMPT",
		"sha":         "GITHUB_SHA",
		"workflow":    "GITHUB_WORKFLOW",
		"job":         "GITHUB_JOB",
	} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			metadata[key] = value
		}
	}
	return metadata
}

func mergeMetadata(base map[string]interface{}, extra map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func executionModel(data []byte) string {
	messages, err := parseSDKMessages(data)
	if err != nil {
		return ""
	}
	for _, message := range messages {
		if model := strings.TrimSpace(stringValue(message["model"])); model != "" {
			return model
		}
		if nested, ok := asMap(message["message"]); ok {
			if model := strings.TrimSpace(stringValue(nested["model"])); model != "" {
				return model
			}
		}
	}
	return ""
}

func ResolveExecutionFile(path string) (string, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return resolved, nil
}
