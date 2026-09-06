package agents

import (
	"context"
	"errors"
	"os"
	"strings"
)

func init() {
	if env := os.Getenv("E2E_AGENT"); env != "github-actions" {
		return
	}
	Register(&GitHubActions{})
	RegisterGate("github-actions", 1)
}

// GitHubActions is a lifecycle adapter for the hosted CI workflow. A local
// lifecycle runner will be added after protocol compliance is established.
type GitHubActions struct{}

func (a *GitHubActions) Name() string               { return "github-actions" }
func (a *GitHubActions) Binary() string             { return "entire-agent-github-actions" }
func (a *GitHubActions) EntireAgent() string        { return "github-actions" }
func (a *GitHubActions) PromptPattern() string      { return "" }
func (a *GitHubActions) TimeoutMultiplier() float64 { return 4.0 }
func (a *GitHubActions) IsExternalAgent() bool      { return true }
func (a *GitHubActions) Bootstrap() error           { return nil }

func (a *GitHubActions) RunPrompt(context.Context, string, string, ...Option) (Output, error) {
	err := errors.New("hosted GitHub Actions lifecycle runner not implemented")
	return Output{Command: "github-actions", ExitCode: -1, Stderr: err.Error()}, err
}

func (a *GitHubActions) StartSession(context.Context, string) (Session, error) {
	return nil, nil
}

func (a *GitHubActions) IsTransientError(out Output, _ error) bool {
	combined := strings.ToLower(out.Stdout + out.Stderr)
	for _, pattern := range []string{"rate limit", "overloaded", "429", "503", "529", "runner unavailable"} {
		if strings.Contains(combined, pattern) {
			return true
		}
	}
	return false
}
