package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/githubactions"
	"github.com/entireio/external-agents/agents/entire-agent-github-actions/internal/protocol"
)

func main() {
	agent := githubactions.New()

	if len(os.Args) < 2 {
		fatalf("usage: entire-agent-github-actions <subcommand> [args]")
	}

	var err error
	switch os.Args[1] {
	case "info":
		err = protocol.WriteJSON(os.Stdout, agent.Info())
	case "detect":
		err = protocol.WriteJSON(os.Stdout, agent.Detect())
	case "get-session-id":
		err = protocol.HandleGetSessionID(os.Stdin, os.Stdout, agent)
	case "get-session-dir":
		err = protocol.HandleGetSessionDir(os.Args[2:], os.Stdout, agent)
	case "resolve-session-file":
		err = protocol.HandleResolveSessionFile(os.Args[2:], os.Stdout, agent)
	case "read-session":
		err = protocol.HandleReadSession(os.Stdin, os.Stdout, agent)
	case "write-session":
		err = protocol.HandleWriteSession(os.Stdin, agent)
	case "read-transcript":
		err = protocol.HandleReadTranscript(os.Args[2:], os.Stdout, agent)
	case "chunk-transcript":
		err = protocol.HandleChunkTranscript(os.Args[2:], os.Stdin, os.Stdout, agent)
	case "reassemble-transcript":
		err = protocol.HandleReassembleTranscript(os.Stdin, os.Stdout, agent)
	case "compact-transcript":
		err = protocol.HandleCompactTranscript(os.Args[2:], os.Stdout, agent)
	case "format-resume-command":
		err = protocol.HandleFormatResumeCommand(os.Args[2:], os.Stdout, agent)
	case "parse-hook":
		err = protocol.HandleParseHook(os.Args[2:], os.Stdin, os.Stdout, agent)
	case "install-hooks":
		err = protocol.HandleInstallHooks(os.Args[2:], os.Stdout, agent)
	case "uninstall-hooks":
		err = agent.UninstallHooks()
	case "are-hooks-installed":
		err = protocol.WriteJSON(os.Stdout, protocol.AreHooksInstalledResponse{Installed: agent.AreHooksInstalled()})
	case "get-transcript-position":
		err = protocol.HandleGetTranscriptPosition(os.Args[2:], os.Stdout, agent)
	case "extract-modified-files":
		err = protocol.HandleExtractModifiedFiles(os.Args[2:], os.Stdout, agent)
	case "extract-prompts":
		err = protocol.HandleExtractPrompts(os.Args[2:], os.Stdout, agent)
	case "extract-summary":
		err = protocol.HandleExtractSummary(os.Args[2:], os.Stdout, agent)
	case "calculate-tokens":
		err = protocol.HandleCalculateTokens(os.Args[2:], os.Stdin, os.Stdout, agent)
	case "capture-start":
		err = handleCaptureStart(os.Args[2:], os.Stdout, agent)
	case "capture-finish":
		err = handleCaptureFinish(os.Args[2:], os.Stdout, agent)
	default:
		fatalf("unknown subcommand: %s", os.Args[1])
	}

	if err != nil {
		fatalf("%v", err)
	}
}

func handleCaptureStart(args []string, stdout io.Writer, agent *githubactions.Agent) error {
	flags := flag.NewFlagSet("capture-start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sessionID := flags.String("session-id", "", "stable workflow session id")
	prompt := flags.String("prompt", "", "task prompt")
	provider := flags.String("provider", "auto", "AI provider: auto, claude, codex, or cursor")
	model := flags.String("model", "", "requested model identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	result, err := agent.CaptureStart(*sessionID, *prompt, *provider, *model)
	if err != nil {
		return err
	}
	return protocol.WriteJSON(stdout, result)
}

func handleCaptureFinish(args []string, stdout io.Writer, agent *githubactions.Agent) error {
	flags := flag.NewFlagSet("capture-finish", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	executionFile := flags.String("execution-file", "", "provider execution JSON or JSONL file")
	sessionID := flags.String("session-id", "", "stable workflow session id")
	prompt := flags.String("prompt", "", "task prompt, used when the provider stream omits it")
	provider := flags.String("provider", "auto", "AI provider: auto, claude, codex, or cursor")
	model := flags.String("model", "", "requested model identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	resolved, err := githubactions.ResolveExecutionFile(*executionFile)
	if err != nil {
		return err
	}
	result, err := agent.CaptureFinish(resolved, *sessionID, *provider, *prompt, *model)
	if err != nil {
		return err
	}
	return protocol.WriteJSON(stdout, result)
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
