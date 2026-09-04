package githubactions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/protocol"
)

const agentName = "github-actions"

var fileModificationTools = map[string]struct{}{
	"edit":         {},
	"multiedit":    {},
	"notebookedit": {},
	"replace":      {},
	"str_replace":  {},
	"write":        {},
	"writefile":    {},
}

var filePathKeys = []string{
	"file_path",
	"filePath",
	"notebook_path",
	"path",
	"target_file",
	"filename",
}

type sdkMessage map[string]any

func (a *Agent) ReadSession(input *protocol.HookInputJSON) (protocol.AgentSessionJSON, error) {
	if input == nil || strings.TrimSpace(input.SessionID) == "" {
		return protocol.AgentSessionJSON{}, errors.New("session id is required")
	}

	sessionRef := strings.TrimSpace(input.SessionRef)
	if sessionRef == "" {
		dir, err := a.GetSessionDir(protocol.RepoRoot())
		if err != nil {
			return protocol.AgentSessionJSON{}, err
		}
		sessionRef = a.ResolveSessionFile(dir, input.SessionID)
	}

	data, err := os.ReadFile(sessionRef)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return protocol.AgentSessionJSON{}, fmt.Errorf("read session transcript: %w", err)
	}
	files, _, err := a.ExtractModifiedFiles(sessionRef, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return protocol.AgentSessionJSON{}, err
	}
	if files == nil {
		files = []string{}
	}
	startTime := input.Timestamp
	if startTime == "" {
		startTime = time.Now().UTC().Format(time.RFC3339)
	}

	return protocol.AgentSessionJSON{
		SessionID:     input.SessionID,
		AgentName:     agentName,
		RepoPath:      protocol.RepoRoot(),
		SessionRef:    sessionRef,
		StartTime:     startTime,
		NativeData:    data,
		ModifiedFiles: files,
		NewFiles:      []string{},
		DeletedFiles:  []string{},
	}, nil
}

func (a *Agent) WriteSession(session protocol.AgentSessionJSON) error {
	if strings.TrimSpace(session.SessionRef) == "" {
		return errors.New("session_ref is required")
	}
	parent := filepath.Dir(session.SessionRef)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	temp, err := os.CreateTemp(parent, ".entire-session-*")
	if err != nil {
		return fmt.Errorf("create temporary session: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("secure temporary session: %w", err)
	}
	if _, err := temp.Write(session.NativeData); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary session: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary session: %w", err)
	}
	if err := os.Rename(tempPath, session.SessionRef); err != nil {
		return fmt.Errorf("publish session: %w", err)
	}
	return nil
}

func (a *Agent) ReadTranscript(sessionRef string) ([]byte, error) {
	if strings.TrimSpace(sessionRef) == "" {
		return nil, errors.New("session_ref is required")
	}
	return os.ReadFile(sessionRef)
}

func (a *Agent) ChunkTranscript(content []byte, maxSize int) ([][]byte, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("max-size must be positive, got %d", maxSize)
	}
	if len(content) == 0 {
		return [][]byte{{}}, nil
	}
	chunks := make([][]byte, 0, (len(content)+maxSize-1)/maxSize)
	for start := 0; start < len(content); start += maxSize {
		end := min(start+maxSize, len(content))
		chunk := make([]byte, end-start)
		copy(chunk, content[start:end])
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func (a *Agent) ReassembleTranscript(chunks [][]byte) ([]byte, error) {
	return bytes.Join(chunks, nil), nil
}

func (a *Agent) GetTranscriptPosition(path string) (int, error) {
	messages, err := readSDKMessages(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(messages), nil
}

func (a *Agent) ExtractModifiedFiles(path string, offset int) ([]string, int, error) {
	messages, err := readSDKMessages(path)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	selected := fromOffset(messages, offset)
	seen := make(map[string]struct{})
	for _, message := range selected {
		for _, block := range contentBlocks(message) {
			if strings.ToLower(stringValue(block["type"])) != "tool_use" {
				continue
			}
			if _, ok := fileModificationTools[strings.ToLower(stringValue(block["name"]))]; !ok {
				continue
			}
			input, _ := block["input"].(map[string]any)
			for _, key := range filePathKeys {
				path := strings.TrimSpace(stringValue(input[key]))
				if path != "" {
					seen[filepath.Clean(path)] = struct{}{}
					break
				}
			}
		}
	}
	files := make([]string, 0, len(seen))
	for path := range seen {
		files = append(files, path)
	}
	sort.Strings(files)
	return files, len(messages), nil
}

func (a *Agent) ExtractPrompts(sessionRef string, offset int) ([]string, error) {
	messages, err := readSDKMessages(sessionRef)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	prompts := []string{}
	for _, message := range fromOffset(messages, offset) {
		if strings.ToLower(stringValue(message["type"])) != "user" {
			continue
		}
		if text := messageText(message); text != "" {
			prompts = append(prompts, text)
		}
	}
	return prompts, nil
}

func (a *Agent) ExtractSummary(sessionRef string) (string, bool, error) {
	messages, err := readSDKMessages(sessionRef)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if strings.ToLower(stringValue(message["type"])) == "result" {
			if result := strings.TrimSpace(stringValue(message["result"])); result != "" {
				return result, true, nil
			}
		}
		if strings.ToLower(stringValue(message["type"])) == "assistant" {
			if text := messageText(message); text != "" {
				return text, true, nil
			}
		}
	}
	return "", false, nil
}

func (a *Agent) CalculateTokens(data []byte, offset int) (protocol.TokenUsageResponse, error) {
	messages, err := parseSDKMessages(data)
	if err != nil {
		return protocol.TokenUsageResponse{}, err
	}
	var total protocol.TokenUsageResponse
	for _, message := range fromOffset(messages, offset) {
		if usage, ok := asMap(message["usage"]); ok {
			addUsage(&total, usage)
			continue
		}
		if nested, ok := asMap(message["message"]); ok {
			if usage, ok := asMap(nested["usage"]); ok {
				addUsage(&total, usage)
			}
		}
		if modelUsage, ok := asMap(message["modelUsage"]); ok {
			for _, model := range modelUsage {
				if usage, ok := asMap(model); ok {
					addUsage(&total, usage)
				}
			}
		}
	}
	return total, nil
}

func (a *Agent) CompactTranscript(sessionRef string) (protocol.CompactTranscriptResponse, error) {
	messages, err := readSDKMessages(sessionRef)
	if err != nil {
		return protocol.CompactTranscriptResponse{}, err
	}
	var output bytes.Buffer
	for _, message := range messages {
		kind := strings.ToLower(stringValue(message["type"]))
		if kind != "user" && kind != "assistant" {
			continue
		}
		content := compactContent(message)
		if len(content) == 0 {
			continue
		}
		line := map[string]any{
			"v":           1,
			"agent":       agentName,
			"cli_version": "unknown",
			"type":        kind,
			"content":     content,
		}
		encoded, err := json.Marshal(line)
		if err != nil {
			return protocol.CompactTranscriptResponse{}, err
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}
	return protocol.CompactTranscriptResponse{
		Transcript: base64.StdEncoding.EncodeToString(output.Bytes()),
	}, nil
}

func readSDKMessages(path string) ([]sdkMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSDKMessages(data)
}

func parseSDKMessages(data []byte) ([]sdkMessage, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return []sdkMessage{}, nil
	}
	if data[0] == '[' {
		var messages []sdkMessage
		if err := json.Unmarshal(data, &messages); err != nil {
			return nil, fmt.Errorf("parse Claude execution array: %w", err)
		}
		return messages, nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse Claude execution object: %w", err)
	}
	if rawMessages, ok := envelope["messages"].([]any); ok {
		messages := make([]sdkMessage, 0, len(rawMessages))
		for _, raw := range rawMessages {
			if message, ok := asMap(raw); ok {
				messages = append(messages, sdkMessage(message))
			}
		}
		return messages, nil
	}
	if len(envelope) == 0 || stringValue(envelope["type"]) == "" {
		return []sdkMessage{}, nil
	}
	return []sdkMessage{sdkMessage(envelope)}, nil
}

func fromOffset(messages []sdkMessage, offset int) []sdkMessage {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(messages) {
		return nil
	}
	return messages[offset:]
}

func messageText(message sdkMessage) string {
	if nested, ok := asMap(message["message"]); ok {
		return textFromContent(nested["content"])
	}
	return textFromContent(message["content"])
}

func textFromContent(content any) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text)
	}
	blocks, ok := content.([]any)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for _, raw := range blocks {
		block, ok := asMap(raw)
		if !ok || strings.ToLower(stringValue(block["type"])) != "text" {
			continue
		}
		if text := strings.TrimSpace(stringValue(block["text"])); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

func contentBlocks(message sdkMessage) []map[string]any {
	container := map[string]any(message)
	if nested, ok := asMap(message["message"]); ok {
		container = nested
	}
	blocks, _ := container["content"].([]any)
	result := make([]map[string]any, 0, len(blocks))
	for _, raw := range blocks {
		if block, ok := asMap(raw); ok {
			result = append(result, block)
		}
	}
	return result
}

func compactContent(message sdkMessage) []any {
	container := map[string]any(message)
	if nested, ok := asMap(message["message"]); ok {
		container = nested
	}
	if text, ok := container["content"].(string); ok && strings.TrimSpace(text) != "" {
		return []any{map[string]any{"text": text}}
	}
	blocks, _ := container["content"].([]any)
	result := make([]any, 0, len(blocks))
	for _, raw := range blocks {
		block, ok := asMap(raw)
		if !ok {
			continue
		}
		typeName := strings.ToLower(stringValue(block["type"]))
		if typeName == "text" || typeName == "tool_use" || typeName == "tool_result" {
			result = append(result, block)
		}
	}
	return result
}

func addUsage(total *protocol.TokenUsageResponse, usage map[string]any) {
	input := intValue(firstValue(usage, "input_tokens", "inputTokens"))
	output := intValue(firstValue(usage, "output_tokens", "outputTokens"))
	cacheCreation := intValue(firstValue(usage, "cache_creation_input_tokens", "cacheCreationInputTokens", "cache_creation_tokens"))
	cacheRead := intValue(firstValue(usage, "cache_read_input_tokens", "cacheReadInputTokens", "cache_read_tokens"))
	if input == 0 && output == 0 && cacheCreation == 0 && cacheRead == 0 {
		return
	}
	total.InputTokens += input
	total.OutputTokens += output
	total.CacheCreationTokens += cacheCreation
	total.CacheReadTokens += cacheRead
	total.APICallCount++
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, exists := values[key]; exists {
			return value
		}
	}
	return nil
}

func asMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown-session"
	}
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character == '-' || character == '_':
			builder.WriteRune(character)
		case unicode.IsLetter(character) || unicode.IsNumber(character):
			builder.WriteRune(character)
		default:
			builder.WriteByte('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "unknown-session"
	}
	return result
}
