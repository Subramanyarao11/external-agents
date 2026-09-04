package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/entireio/external-agents/proofgate/contracts"
	"github.com/entireio/external-agents/proofgate/engine"
)

type ciGateOptions struct {
	build         buildOptions
	resultPath    string
	githubOutput  string
	githubSummary string
	failOn        string
}

type policyFailure struct {
	decision engine.Decision
}

func (failure policyFailure) Error() string {
	return fmt.Sprintf("ProofGate policy rejected this change with decision %s", failure.decision)
}

func ciGate(args []string, stdout io.Writer) error {
	options, err := parseCIGateOptions(args)
	if err != nil {
		return err
	}
	passport, now, err := buildFromOptions(options.build)
	if err != nil {
		return err
	}
	result, err := engine.Evaluate(passport, engine.DefaultPolicy(), now)
	if err != nil {
		return err
	}
	output := gateOutput{Passport: passport, Result: result}

	if err := writeJSONFileAtomic(options.resultPath, output); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	if options.githubOutput != "" {
		if err := writeGitHubOutput(options.githubOutput, options.resultPath, passport, result); err != nil {
			return fmt.Errorf("write GitHub output: %w", err)
		}
	}
	if options.githubSummary != "" {
		if err := appendGitHubSummary(options.githubSummary, passport, result); err != nil {
			return fmt.Errorf("write GitHub summary: %w", err)
		}
	}

	_, _ = fmt.Fprintf(stdout, "ProofGate: %s (risk score %d, %d hard stops)\n", result.Decision, result.Score, len(result.HardStops))
	if shouldFail(options.failOn, result.Decision) {
		return policyFailure{decision: result.Decision}
	}
	return nil
}

func parseCIGateOptions(args []string) (ciGateOptions, error) {
	flags := flag.NewFlagSet("ci-gate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options ciGateOptions
	flags.StringVar(&options.build.repositoryPath, "repo", ".", "path to the git repository")
	flags.StringVar(&options.build.repositoryID, "repo-id", "", "synthetic or approved repository identifier")
	flags.StringVar(&options.build.commit, "commit", "HEAD", "commit or revision to inspect")
	flags.StringVar(&options.build.checkpointID, "checkpoint", "auto", "Entire checkpoint ID, or auto for commit trailer")
	flags.StringVar(&options.build.intent, "intent", "", "safe intent summary; defaults to commit subject")
	flags.StringVar(&options.build.testsPath, "tests", "", "ProofGate test report JSON")
	flags.BoolVar(&options.build.repositoryOptedIn, "repo-opted-in", false, "confirm repository opt-in for governed export")
	flags.BoolVar(&options.build.repositoryIsPublic, "repo-public", false, "mark repository as public")
	flags.BoolVar(&options.build.aiAuthored, "ai-authored", true, "mark the change as AI-authored")
	flags.StringVar(&options.build.sensitivePrefixes, "sensitive-prefixes", "", "comma-separated sensitive path prefixes")
	flags.StringVar(&options.build.deniedPrefixes, "denied-prefixes", "", "comma-separated never-auto-approve path prefixes")
	flags.StringVar(&options.build.nowValue, "now", "", "evaluation time in RFC3339")
	flags.StringVar(&options.resultPath, "result", "proofgate-result.json", "machine-readable result path")
	flags.StringVar(&options.githubOutput, "github-output", os.Getenv("GITHUB_OUTPUT"), "GitHub Actions output file")
	flags.StringVar(&options.githubSummary, "github-summary", os.Getenv("GITHUB_STEP_SUMMARY"), "GitHub Actions job-summary file")
	flags.StringVar(&options.failOn, "fail-on", "approval-required", "approval-required, warn, or never")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	options.resultPath = strings.TrimSpace(options.resultPath)
	if options.resultPath == "" {
		return options, errors.New("--result is required")
	}
	switch options.failOn {
	case "approval-required", "warn", "never":
	default:
		return options, fmt.Errorf("invalid --fail-on %q", options.failOn)
	}
	return options, nil
}

func shouldFail(threshold string, decision engine.Decision) bool {
	switch threshold {
	case "approval-required":
		return decision == engine.DecisionApprovalRequired
	case "warn":
		return decision == engine.DecisionWarn || decision == engine.DecisionApprovalRequired
	default:
		return false
	}
}

func writeJSONFileAtomic(path string, value any) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	directory := filepath.Dir(absolute)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(directory, ".proofgate-result-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, absolute)
}

func writeGitHubOutput(path, resultPath string, passport contracts.ChangePassport, result engine.Result) error {
	values := map[string]string{
		"checkpoint_id": passport.Change.CheckpointID,
		"decision":      string(result.Decision),
		"hard_stops":    strconv.Itoa(len(result.HardStops)),
		"result_path":   resultPath,
		"risk_score":    strconv.Itoa(result.Score),
	}
	keys := []string{"decision", "risk_score", "hard_stops", "checkpoint_id", "result_path"}
	var output strings.Builder
	for _, key := range keys {
		value := values[key]
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("output %s contains a newline", key)
		}
		_, _ = fmt.Fprintf(&output, "%s=%s\n", key, value)
	}
	return appendFile(path, output.String())
}

func appendGitHubSummary(path string, passport contracts.ChangePassport, result engine.Result) error {
	var summary strings.Builder
	_, _ = fmt.Fprintf(&summary, "## ProofGate: %s\n\n", result.Decision)
	_, _ = fmt.Fprintf(&summary, "| Evidence | Value |\n| --- | --- |\n")
	_, _ = fmt.Fprintf(&summary, "| Risk score | %d |\n", result.Score)
	_, _ = fmt.Fprintf(&summary, "| Hard stops | %d |\n", len(result.HardStops))
	_, _ = fmt.Fprintf(&summary, "| Entire checkpoint | `%s` |\n", markdownCell(passport.Change.CheckpointID, "missing"))
	_, _ = fmt.Fprintf(&summary, "| Source adapter | %s |\n", markdownCell(passport.Authoring.SourceAdapter, "unknown"))
	_, _ = fmt.Fprintf(&summary, "| Provenance complete | %t |\n", passport.Authoring.ProvenanceComplete)
	_, _ = fmt.Fprintf(&summary, "| Change size | %d files / %d changed lines |\n", len(passport.Impact.ChangedFiles), passport.Impact.ChangedLineCount)
	_, _ = fmt.Fprintf(&summary, "| Tests | %d total / %d failed |\n\n", passport.Tests.Total, passport.Tests.Failed)

	if len(result.HardStops) > 0 {
		summary.WriteString("### Required action\n\n")
		for _, stop := range result.HardStops {
			_, _ = fmt.Fprintf(&summary, "- **%s:** %s\n", markdownText(stop.Code), markdownText(stop.Message))
		}
		summary.WriteString("\n")
	}
	if len(result.Reasons) > 0 {
		summary.WriteString("<details><summary>Risk evidence</summary>\n\n")
		for _, reason := range result.Reasons {
			_, _ = fmt.Fprintf(&summary, "- **%s** (+%d): %s\n", markdownText(reason.Code), reason.Points, markdownText(reason.Message))
		}
		summary.WriteString("\n</details>\n\n")
	}
	summary.WriteString("> The verdict is deterministic. AI-generated explanations are advisory and cannot change it.\n")
	return appendFile(path, summary.String())
}

func appendFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = io.WriteString(file, content)
	return err
}

func markdownCell(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return markdownText(value)
}

func markdownText(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "`", "\\`", "|", "\\|", "\r", " ", "\n", " ")
	return replacer.Replace(value)
}
