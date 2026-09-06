// Package githubactions implements the Entire external agent protocol for AI
// coding sessions running in GitHub Actions.
package githubactions

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

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

func (a *Agent) GetSessionDir(repoPath string) (string, error) {
	base := os.Getenv("RUNNER_TEMP")
	if base != "" {
		return filepath.Join(base, "entire-github-actions", "sessions"), nil
	}
	if strings.TrimSpace(repoPath) == "" {
		repoPath = protocol.RepoRoot()
	}
	absolute, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(absolute))
	repoKey := hex.EncodeToString(digest[:6])
	return filepath.Join(os.TempDir(), "entire-github-actions", repoKey, "sessions"), nil
}

func (a *Agent) ResolveSessionFile(sessionDir, sessionID string) string {
	return filepath.Join(sessionDir, safeFilename(sessionID)+".json")
}

func (a *Agent) FormatResumeCommand(sessionID string) string {
	return "gh run rerun -- " + shellQuote(sessionID)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
