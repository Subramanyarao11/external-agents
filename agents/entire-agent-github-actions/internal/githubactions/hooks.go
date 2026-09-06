package githubactions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/protocol"
)

const (
	actionRelativePath = ".github/actions/entire-proofgate/action.yml"
	actionMarker       = "# Managed by entire-agent-github-actions."
)

func (a *Agent) ParseHook(hookName string, input []byte) (*protocol.EventJSON, error) {
	input = bytes.TrimSpace(input)
	if len(input) == 0 {
		return nil, nil
	}
	var raw protocol.HookInputJSON
	if err := json.Unmarshal(input, &raw); err != nil {
		return nil, fmt.Errorf("parse GitHub Actions hook input: %w", err)
	}
	raw.SessionID = strings.TrimSpace(raw.SessionID)
	if raw.SessionID == "" {
		return nil, nil
	}
	if raw.Timestamp == "" {
		raw.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if raw.SessionRef == "" {
		dir, err := a.GetSessionDir(protocol.RepoRoot())
		if err != nil {
			return nil, err
		}
		raw.SessionRef = a.ResolveSessionFile(dir, raw.SessionID)
	}

	event := &protocol.EventJSON{
		SessionID:  raw.SessionID,
		SessionRef: raw.SessionRef,
		Timestamp:  raw.Timestamp,
		Model:      rawString(raw.RawData, "model"),
		Metadata:   actionMetadata(raw.RawData),
	}
	switch hookName {
	case HookRunStart:
		event.Type = 1
	case HookTurnStart:
		event.Type = 2
		event.Prompt = raw.UserPrompt
	case HookTurnEnd:
		event.Type = 3
		event.ResponseMessage = rawString(raw.RawData, "response_message")
	case HookRunEnd:
		event.Type = 5
	default:
		return nil, nil
	}
	return event, nil
}

func (a *Agent) InstallHooks(localDev, force bool) (int, error) {
	path := filepath.Join(protocol.RepoRoot(), actionRelativePath)
	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, []byte(generatedAction(localDev))) {
			return 0, nil
		}
		if !force && !bytes.Contains(existing, []byte(actionMarker)) {
			return 0, fmt.Errorf("refusing to overwrite unmanaged action %s; rerun with --force", actionRelativePath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read existing action: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return 0, fmt.Errorf("create action directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(generatedAction(localDev)), 0o600); err != nil {
		return 0, fmt.Errorf("write action: %w", err)
	}
	return 1, nil
}

func (a *Agent) UninstallHooks() error {
	path := filepath.Join(protocol.RepoRoot(), actionRelativePath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte(actionMarker)) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	removeEmptyParents(filepath.Dir(path), filepath.Join(protocol.RepoRoot(), ".github"))
	return nil
}

func (a *Agent) AreHooksInstalled() bool {
	data, err := os.ReadFile(filepath.Join(protocol.RepoRoot(), actionRelativePath))
	return err == nil && bytes.Contains(data, []byte(actionMarker))
}

func generatedAction(localDev bool) string {
	binary := "entire-agent-github-actions"
	if localDev {
		binary = filepath.Join(protocol.RepoRoot(), "agents", "entire-agent-github-actions", "entire-agent-github-actions")
	}
	return fmt.Sprintf(`%s
name: Entire ProofGate capture
description: Capture a Claude, Codex, or Cursor execution as an Entire session
inputs:
  mode:
    description: Use begin before the AI action and finish after it
    required: true
  execution_file:
    description: Path to provider execution JSON or JSONL
    required: false
    default: ""
  provider:
    description: AI provider (auto, claude, codex, or cursor)
    required: false
    default: auto
  model:
    description: Requested model identifier when the stream omits it
    required: false
    default: ""
  session_id:
    description: Stable run-scoped id shared by the begin and finish steps
    required: true
  prompt:
    description: Redacted task prompt recorded for the turn
    required: false
    default: ""
runs:
  using: composite
  steps:
    - name: Begin Entire session
      if: inputs.mode == 'begin'
      shell: bash
      env:
        ENTIRE_SESSION_ID: ${{ inputs.session_id }}
        ENTIRE_TASK_PROMPT: ${{ inputs.prompt }}
        ENTIRE_PROVIDER: ${{ inputs.provider }}
        ENTIRE_MODEL: ${{ inputs.model }}
      run: |
        %s capture-start \
          --session-id "$ENTIRE_SESSION_ID" \
          --prompt "$ENTIRE_TASK_PROMPT" \
          --provider "$ENTIRE_PROVIDER" \
          --model "$ENTIRE_MODEL"
    - name: Finish Entire session
      if: inputs.mode == 'finish'
      shell: bash
      env:
        ENTIRE_EXECUTION_FILE: ${{ inputs.execution_file }}
        ENTIRE_SESSION_ID: ${{ inputs.session_id }}
        ENTIRE_TASK_PROMPT: ${{ inputs.prompt }}
        ENTIRE_PROVIDER: ${{ inputs.provider }}
        ENTIRE_MODEL: ${{ inputs.model }}
      run: |
        %s capture-finish \
          --execution-file "$ENTIRE_EXECUTION_FILE" \
          --session-id "$ENTIRE_SESSION_ID" \
          --prompt "$ENTIRE_TASK_PROMPT" \
          --provider "$ENTIRE_PROVIDER" \
          --model "$ENTIRE_MODEL"
`, actionMarker, binary, binary)
}

func removeEmptyParents(start, stop string) {
	for current := start; current != stop && strings.HasPrefix(current, stop); current = filepath.Dir(current) {
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) != 0 {
			return
		}
		if err := os.Remove(current); err != nil {
			return
		}
	}
}

func rawString(raw map[string]interface{}, key string) string {
	value, _ := raw[key].(string)
	return value
}

func actionMetadata(raw map[string]interface{}) map[string]string {
	metadata := map[string]string{}
	for _, key := range []string{"repository", "run_id", "run_attempt", "sha", "workflow", "job", "provider"} {
		value := strings.TrimSpace(rawString(raw, key))
		if value != "" {
			metadata[key] = value
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}
