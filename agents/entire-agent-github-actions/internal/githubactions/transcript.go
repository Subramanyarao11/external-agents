package githubactions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/protocol"
)

const (
	agentName            = "github-actions"
	messageTypeUser      = "user"
	messageTypeAssistant = "assistant"
)

var fileModificationTools = map[string]struct{}{
	"edit":         {},
	"file_changed": {},
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
				path := safeEvidencePath(stringValue(input[key]))
				if path != "" {
					seen[path] = struct{}{}
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
		if strings.ToLower(stringValue(message["type"])) != messageTypeUser {
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
		if strings.ToLower(stringValue(message["type"])) == messageTypeAssistant {
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
		if kind != messageTypeUser && kind != messageTypeAssistant {
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
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []sdkMessage{}, nil
	}
	if trimmed[0] == '[' {
		var messages []sdkMessage
		if err := json.Unmarshal(trimmed, &messages); err != nil {
			return nil, fmt.Errorf("parse execution array: %w", err)
		}
		return normalizeExecutionRecords(messages), nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(trimmed, &envelope); err == nil {
		return normalizeExecutionRecords(messagesFromObject(envelope)), nil
	}
	records, err := parseEventJSONL(data)
	if err != nil {
		return nil, err
	}
	return normalizeExecutionRecords(records), nil
}

func messagesFromObject(envelope map[string]any) []sdkMessage {
	if rawMessages, ok := envelope["messages"].([]any); ok {
		messages := make([]sdkMessage, 0, len(rawMessages))
		for _, raw := range rawMessages {
			if message, ok := asMap(raw); ok {
				messages = append(messages, sdkMessage(message))
			}
		}
		return messages
	}
	if stringValue(envelope["event"]) != "" {
		if message, ok := normalizeEventRecord(envelope); ok {
			return []sdkMessage{message}
		}
		return []sdkMessage{}
	}
	if len(envelope) == 0 || stringValue(envelope["type"]) == "" {
		return []sdkMessage{}
	}
	return []sdkMessage{sdkMessage(envelope)}
}

func parseEventJSONL(data []byte) ([]sdkMessage, error) {
	lines := bytes.Split(data, []byte("\n"))
	lastContentLine := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(lines[i])) != 0 {
			lastContentLine = i
			break
		}
	}

	messages := make([]sdkMessage, 0, len(lines))
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			unterminatedTail := i == lastContentLine && !bytes.HasSuffix(data, []byte("\n")) && jsonRecordIsIncomplete(line)
			if unterminatedTail {
				return messages, nil
			}
			return nil, fmt.Errorf("parse Claude execution JSONL line %d: %w", i+1, err)
		}
		if stringValue(record["event"]) != "" {
			if message, ok := normalizeEventRecord(record); ok {
				messages = append(messages, message)
			}
			continue
		}
		if stringValue(record["type"]) != "" {
			messages = append(messages, sdkMessage(record))
		}
	}
	return messages, nil
}

func jsonRecordIsIncomplete(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value any
	return errors.Is(decoder.Decode(&value), io.ErrUnexpectedEOF)
}

func normalizeEventRecord(record map[string]any) (sdkMessage, bool) {
	message := make(sdkMessage, len(record)+3)
	for key, value := range record {
		message[key] = value
	}

	switch stringValue(record["event"]) {
	case "session_started":
		message["type"] = "system"
		message["subtype"] = "init"
	case "user_prompt":
		message["type"] = messageTypeUser
		message["message"] = textMessage(messageTypeUser, stringValue(record["text"]))
	case "agent_response":
		message["type"] = messageTypeAssistant
		message["message"] = textMessage(messageTypeAssistant, stringValue(record["text"]))
	case "tool_call":
		message["type"] = messageTypeAssistant
		message["message"] = contentMessage(messageTypeAssistant, map[string]any{
			"type":  "tool_use",
			"id":    record["call_id"],
			"name":  record["tool"],
			"input": record["input"],
		})
	case "tool_result":
		message["type"] = messageTypeUser
		message["message"] = contentMessage(messageTypeUser, map[string]any{
			"type":        "tool_result",
			"tool_use_id": record["call_id"],
			"content":     record["output"],
		})
	case "file_read":
		message["type"] = messageTypeAssistant
		message["message"] = contentMessage(messageTypeAssistant, map[string]any{
			"type": "tool_use",
			"name": "file_read",
			"input": map[string]any{
				"file_path": record["path"],
				"lines":     record["lines"],
			},
		})
	case "file_changed":
		message["type"] = messageTypeAssistant
		message["message"] = contentMessage(messageTypeAssistant, map[string]any{
			"type": "tool_use",
			"name": "file_changed",
			"input": map[string]any{
				"file_path":     record["path"],
				"change":        record["change"],
				"summary":       record["summary"],
				"lines_added":   record["lines_added"],
				"lines_removed": record["lines_removed"],
			},
		})
	case "usage":
		message["type"] = "usage"
		message["usage"] = map[string]any{
			"input_tokens":  record["input_tokens"],
			"output_tokens": record["output_tokens"],
		}
	case "checkpoint_created":
		message["type"] = "result"
		message["subtype"] = "success"
		message["result"] = record["summary"]
	case "session_ended":
		message["type"] = "system"
		message["subtype"] = "session_ended"
	default:
		return nil, false
	}
	return message, true
}

func textMessage(role, text string) map[string]any {
	return contentMessage(role, map[string]any{"type": "text", "text": text})
}

func contentMessage(role string, content map[string]any) map[string]any {
	return map[string]any{
		"role":    role,
		"content": []any{content},
	}
}

func normalizeExecutionRecords(records []sdkMessage) []sdkMessage {
	if looksLikeCodexRollout(records) {
		return normalizeCodexRollout(records)
	}
	if looksLikeCodexExec(records) {
		return normalizeCodexExec(records)
	}
	return normalizeCursorRecords(records)
}

func looksLikeCodexExec(records []sdkMessage) bool {
	for _, record := range records {
		switch stringValue(record["type"]) {
		case "thread.started", "turn.started", "turn.completed", "turn.failed", "item.started", "item.completed", "item.updated":
			return true
		}
	}
	return false
}

func looksLikeCodexRollout(records []sdkMessage) bool {
	for _, record := range records {
		switch stringValue(record["type"]) {
		case "session_meta", "turn_context", "response_item", "event_msg", "compacted":
			return true
		}
	}
	return false
}

func normalizeCodexExec(records []sdkMessage) []sdkMessage {
	normalized := make([]sdkMessage, 0, len(records))
	lastAssistant := ""
	for _, record := range records {
		switch stringValue(record["type"]) {
		case "thread.started":
			normalized = append(normalized, sdkMessage{
				"type":       "system",
				"subtype":    "init",
				"session_id": stringValue(record["thread_id"]),
			})
		case "turn.started":
			normalized = append(normalized, record)
		case "item.started", "item.completed", "item.updated":
			item, _ := asMap(record["item"])
			message := normalizeCodexItem(item)
			if message == nil {
				normalized = append(normalized, record)
				continue
			}
			if text := messageText(message); text != "" {
				lastAssistant = text
			}
			normalized = append(normalized, message)
		case "turn.completed":
			normalized = append(normalized, sdkMessage{
				"type":    "result",
				"subtype": "success",
				"result":  lastAssistant,
				"usage":   record["usage"],
			})
		case "turn.failed", "error":
			normalized = append(normalized, sdkMessage{
				"type":     "result",
				"subtype":  "error",
				"is_error": true,
				"result":   codexErrorText(record),
			})
		default:
			normalized = append(normalized, record)
		}
	}
	return normalized
}

func normalizeCodexRollout(records []sdkMessage) []sdkMessage {
	normalized := make([]sdkMessage, 0, len(records))
	var latestUsage map[string]any
	for _, record := range records {
		payload, _ := asMap(record["payload"])
		switch stringValue(record["type"]) {
		case "session_meta", "turn_context":
			normalized = append(normalized, sdkMessage{
				"type":       "system",
				"subtype":    "init",
				"session_id": firstString(payload, "id", "session_id", "thread_id"),
				"model":      stringValue(payload["model"]),
			})
		case "response_item":
			if stringValue(payload["type"]) != "message" {
				continue
			}
			role := strings.ToLower(stringValue(payload["role"]))
			if role != "user" && role != "assistant" {
				continue
			}
			normalized = append(normalized, sdkMessage{
				"type":    role,
				"message": map[string]any{"role": role, "content": normalizeTextBlocks(payload["content"])},
			})
		case "event_msg":
			switch stringValue(payload["type"]) {
			case "item_completed":
				item, _ := asMap(payload["item"])
				if message := normalizeCodexItem(item); message != nil {
					normalized = append(normalized, message)
				}
			case "patch_apply_end":
				// Persisted Codex rollouts report successful apply_patch file
				// changes here rather than as an item_completed/file_change pair.
				// Reuse the same allowlisted path-only representation and never
				// retain patch bodies, stdout or stderr.
				if stringValue(payload["status"]) == "completed" || boolValue(payload["success"]) {
					if blocks := fileChangeBlocks(payload["changes"]); len(blocks) != 0 {
						normalized = append(normalized, sdkMessage{"type": "assistant", "content": blocks})
					}
				}
			case "token_count":
				info, _ := asMap(payload["info"])
				usage, _ := asMap(info["total_token_usage"])
				if len(usage) != 0 {
					latestUsage = usage
				}
			case "task_complete":
				normalized = append(normalized, sdkMessage{
					"type":    "result",
					"subtype": "success",
					"result":  stringValue(payload["last_agent_message"]),
				})
			}
		}
	}
	if len(latestUsage) != 0 {
		normalized = append(normalized, sdkMessage{"type": "usage", "usage": latestUsage})
	}
	return normalized
}

func normalizeCodexItem(item map[string]any) sdkMessage {
	switch stringValue(item["type"]) {
	case "agent_message":
		text := strings.TrimSpace(stringValue(item["text"]))
		if text == "" {
			return nil
		}
		return sdkMessage{"type": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}
	case "file_change":
		blocks := fileChangeBlocks(item["changes"])
		if len(blocks) == 0 {
			return nil
		}
		return sdkMessage{"type": "assistant", "content": blocks}
	case "command_execution":
		command := stringValue(item["command"])
		if command == "" {
			if parts, ok := item["command"].([]any); ok {
				values := make([]string, 0, len(parts))
				for _, part := range parts {
					if value := stringValue(part); value != "" {
						values = append(values, value)
					}
				}
				command = strings.Join(values, " ")
			}
		}
		return sdkMessage{"type": "assistant", "content": []any{map[string]any{
			"type": "tool_use", "name": "Shell", "input": map[string]any{"command": command},
		}}}
	default:
		return nil
	}
}

func fileChangeBlocks(changes any) []any {
	paths := []string{}
	switch typed := changes.(type) {
	case []any:
		for _, raw := range typed {
			change, _ := asMap(raw)
			if path := safeEvidencePath(firstString(change, "path", "file_path", "filePath")); path != "" {
				paths = append(paths, path)
			}
		}
	case map[string]any:
		for path := range typed {
			if path := safeEvidencePath(path); path != "" {
				paths = append(paths, path)
			}
		}
	}
	sort.Strings(paths)
	blocks := make([]any, 0, len(paths))
	for _, path := range paths {
		blocks = append(blocks, map[string]any{
			"type": "tool_use", "name": "Write", "input": map[string]any{"file_path": path},
		})
	}
	return blocks
}

func normalizeCursorRecords(records []sdkMessage) []sdkMessage {
	normalized := make([]sdkMessage, 0, len(records))
	for _, record := range records {
		if stringValue(record["type"]) != "tool_call" {
			normalized = append(normalized, record)
			continue
		}
		toolCall, _ := asMap(record["tool_call"])
		name, details := firstMapEntry(toolCall)
		args, _ := asMap(details["args"])
		blockType := "tool_use"
		block := map[string]any{
			"type":  blockType,
			"id":    stringValue(record["call_id"]),
			"name":  normalizeCursorToolName(name),
			"input": args,
		}
		if stringValue(record["subtype"]) != "started" {
			block = map[string]any{
				"type":        "tool_result",
				"tool_use_id": stringValue(record["call_id"]),
			}
		}
		normalized = append(normalized, sdkMessage{"type": "assistant", "content": []any{block}})
	}
	return normalized
}

func normalizeCursorToolName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "notebook"):
		return "NotebookEdit"
	case strings.Contains(lower, "write"):
		return "Write"
	case strings.Contains(lower, "edit"), strings.Contains(lower, "replace"):
		return "Edit"
	case strings.Contains(lower, "shell"), strings.Contains(lower, "command"):
		return "Shell"
	case strings.Contains(lower, "read"):
		return "Read"
	default:
		return name
	}
}

func normalizeTextBlocks(value any) []any {
	blocks, _ := value.([]any)
	normalized := make([]any, 0, len(blocks))
	for _, raw := range blocks {
		block, ok := asMap(raw)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(stringValue(block["text"])); text != "" {
			normalized = append(normalized, map[string]any{"type": "text", "text": text})
		}
	}
	return normalized
}

func codexErrorText(record sdkMessage) string {
	if message := strings.TrimSpace(stringValue(record["message"])); message != "" {
		return message
	}
	errorValue, _ := asMap(record["error"])
	return strings.TrimSpace(stringValue(errorValue["message"]))
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(values[key])); value != "" {
			return value
		}
	}
	return ""
}

func firstMapEntry(values map[string]any) (string, map[string]any) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value, ok := asMap(values[key]); ok {
			return key, value
		}
	}
	return "Unknown", map[string]any{}
}

func safeEvidencePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := filepath.Clean(value)
	if filepath.IsAbs(cleaned) {
		relative, err := filepath.Rel(protocol.RepoRoot(), cleaned)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ""
		}
		cleaned = relative
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return ""
	}
	return cleaned
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
	cacheCreation := intValue(firstValue(usage, "cache_creation_input_tokens", "cacheCreationInputTokens", "cache_creation_tokens", "cache_write_input_tokens"))
	cacheRead := intValue(firstValue(usage, "cache_read_input_tokens", "cacheReadInputTokens", "cache_read_tokens", "cached_input_tokens"))
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

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
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
