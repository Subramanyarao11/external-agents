// Package githubactions implements the Entire external agent protocol for AI
// coding sessions running in GitHub Actions.
package githubactions

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/protocol"
)

const (
	HookRunStart  = "run-start"
	HookTurnStart = "turn-start"
	HookTurnEnd   = "turn-end"
	HookRunEnd    = "run-end"
)

type Agent struct{}

func New() *Agent { return &Agent{} }

func (a *Agent) Info() protocol.InfoResponse {
	return protocol.InfoResponse{
		ProtocolVersion: protocol.ProtocolVersion,
		Name:            "github-actions",
		Type:            "GitHub Actions",
		Description:     "Capture AI coding sessions executed in GitHub Actions",
		IsPreview:       true,
		ProtectedDirs:   []string{".github/actions/entire-proofgate"},
		ProtectedFiles:  []string{".github/actions/entire-proofgate/action.yml"},
		HookNames:       []string{HookRunStart, HookTurnStart, HookTurnEnd, HookRunEnd},
		Capabilities: protocol.DeclaredCapabilities{
			Hooks:              true,
			TranscriptAnalyzer: true,
			TokenCalculator:    true,
			CompactTranscript:  true,
		},
	}
}

func (a *Agent) Detect() protocol.DetectResponse {
	return protocol.DetectResponse{Present: os.Getenv("GITHUB_ACTIONS") == "true"}
}

func (a *Agent) GetSessionID(input *protocol.HookInputJSON) string {
	if input == nil {
		return ""
	}
	return input.SessionID
}

func (a *Agent) GetSessionDir(_ string) (string, error) {
	base := os.Getenv("RUNNER_TEMP")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "entire-github-actions", "sessions"), nil
}

func (a *Agent) ResolveSessionFile(sessionDir, sessionID string) string {
	return filepath.Join(sessionDir, sessionID+".json")
}

func (a *Agent) ReadSession(input *protocol.HookInputJSON) (protocol.AgentSessionJSON, error) {
	if input == nil || input.SessionID == "" {
		return protocol.AgentSessionJSON{}, errors.New("session id is required")
	}
	return protocol.AgentSessionJSON{
		SessionID:     input.SessionID,
		AgentName:     "github-actions",
		RepoPath:      protocol.RepoRoot(),
		SessionRef:    input.SessionRef,
		StartTime:     input.Timestamp,
		ModifiedFiles: []string{},
		NewFiles:      []string{},
		DeletedFiles:  []string{},
	}, nil
}

func (a *Agent) WriteSession(protocol.AgentSessionJSON) error {
	return errors.New("write-session not implemented")
}

func (a *Agent) ReadTranscript(sessionRef string) ([]byte, error) {
	return os.ReadFile(sessionRef)
}

func (a *Agent) ChunkTranscript([]byte, int) ([][]byte, error) {
	return nil, errors.New("chunk-transcript not implemented")
}

func (a *Agent) ReassembleTranscript([][]byte) ([]byte, error) {
	return nil, errors.New("reassemble-transcript not implemented")
}

func (a *Agent) CompactTranscript(string) (protocol.CompactTranscriptResponse, error) {
	return protocol.CompactTranscriptResponse{}, errors.New("compact-transcript not implemented")
}

func (a *Agent) FormatResumeCommand(sessionID string) string {
	return "gh run rerun " + sessionID
}

func (a *Agent) ParseHook(string, []byte) (*protocol.EventJSON, error) {
	return nil, nil
}

func (a *Agent) InstallHooks(bool, bool) (int, error) {
	return 0, errors.New("install-hooks not implemented")
}

func (a *Agent) UninstallHooks() error   { return nil }
func (a *Agent) AreHooksInstalled() bool { return false }

func (a *Agent) GetTranscriptPosition(string) (int, error) {
	return 0, errors.New("get-transcript-position not implemented")
}

func (a *Agent) ExtractModifiedFiles(string, int) ([]string, int, error) {
	return nil, 0, errors.New("extract-modified-files not implemented")
}

func (a *Agent) ExtractPrompts(string, int) ([]string, error) {
	return nil, errors.New("extract-prompts not implemented")
}

func (a *Agent) ExtractSummary(string) (string, bool, error) {
	return "", false, errors.New("extract-summary not implemented")
}

func (a *Agent) CalculateTokens([]byte, int) (protocol.TokenUsageResponse, error) {
	return protocol.TokenUsageResponse{}, errors.New("calculate-tokens not implemented")
}
