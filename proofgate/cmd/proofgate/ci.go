package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
	"github.com/entireio/external-agents/proofgate/engine"
	"github.com/entireio/external-agents/proofgate/warehouse"
)

type ciGateOptions struct {
	build              buildOptions
	resultPath         string
	githubOutput       string
	githubSummary      string
	failOn             string
	databricksMode     string
	databricksRequired bool
}

type ciDatabricksState struct {
	Mode             string                    `json:"mode"`
	Status           string                    `json:"status"`
	HistoryAvailable bool                      `json:"history_available"`
	Warnings         []string                  `json:"warnings"`
	Ingest           *warehouse.IngestResult   `json:"ingest,omitempty"`
	Review           *warehouse.ReviewDecision `json:"review,omitempty"`
}

type ciGateOutput struct {
	Passport          contracts.ChangePassport `json:"passport"`
	Result            engine.Result            `json:"result"`
	EnforcementStatus string                   `json:"enforcement_status"`
	Databricks        ciDatabricksState        `json:"databricks"`
}

type policyFailure struct {
	status string
}

func (failure policyFailure) Error() string {
	return fmt.Sprintf("ProofGate enforcement rejected this change with status %s", failure.status)
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
	databricks := ciDatabricksState{
		Mode: options.databricksMode, Status: "OFF", Warnings: []string{},
	}
	var databricksClient *warehouse.Client
	var databricksErrors []error
	if options.databricksMode != "off" {
		databricks.Status = "READY"
		databricksClient, err = warehouseClientFromEnvironment()
		if err != nil {
			databricks.Status = "DEGRADED"
			databricks.Warnings = append(databricks.Warnings, "DATABRICKS_CONFIGURATION_UNAVAILABLE")
			databricksErrors = append(databricksErrors, err)
		}
	}
	if databricksClient != nil && modeNeedsHistory(options.databricksMode) {
		ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
		history, historyErr := databricksClient.LookupHistory(ctx, warehouse.HistoryQuery{
			RepositoryID:        passport.Repository.ID,
			ExcludeEventID:      passport.EventID,
			Before:              passport.OccurredAt,
			SnapshotAt:          now,
			BaselineWindowDays:  30,
			ChangedFileCount:    len(passport.Impact.ChangedFiles),
			ImpactedEntityCount: len(passport.Impact.ImpactedEntities),
			DependencyDepth:     passport.Impact.DependencyDepth,
			SensitiveComponents: passport.Impact.SensitiveComponents,
		})
		cancel()
		if historyErr != nil {
			databricks.Status = "DEGRADED"
			databricks.Warnings = append(databricks.Warnings, "DATABRICKS_HISTORY_UNAVAILABLE")
			databricksErrors = append(databricksErrors, historyErr)
		} else {
			passport.History = history
			databricks.HistoryAvailable = true
		}
	}
	result, err := engine.Evaluate(passport, engine.DefaultPolicy(), now)
	if err != nil {
		return err
	}
	if databricksClient != nil && modeNeedsHistory(options.databricksMode) {
		ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
		review, reviewErr := databricksClient.LookupReview(ctx, passport.EventID)
		cancel()
		if reviewErr != nil {
			databricks.Status = "DEGRADED"
			databricks.Warnings = append(databricks.Warnings, "DATABRICKS_REVIEW_UNAVAILABLE")
			databricksErrors = append(databricksErrors, reviewErr)
		} else if review.Available {
			databricks.Review = &review
		}
	}
	if databricksClient != nil && modeNeedsExport(options.databricksMode) {
		event, eventErr := warehouse.BuildEvent(passport, result)
		if eventErr != nil {
			databricks.Status = "DEGRADED"
			databricks.Warnings = append(databricks.Warnings, "DATABRICKS_EXPORT_REFUSED")
			databricksErrors = append(databricksErrors, eventErr)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
			ingest, ingestErr := databricksClient.Ingest(ctx, event)
			cancel()
			if ingestErr != nil {
				databricks.Status = "DEGRADED"
				databricks.Warnings = append(databricks.Warnings, "DATABRICKS_EXPORT_UNAVAILABLE")
				databricksErrors = append(databricksErrors, ingestErr)
			} else {
				databricks.Ingest = &ingest
			}
		}
	}
	if databricks.Status != "DEGRADED" {
		databricks.Status = databricksSuccessStatus(options.databricksMode)
	}
	enforcement := enforcementStatus(result.Decision, databricks.Review)
	output := ciGateOutput{
		Passport: passport, Result: result, EnforcementStatus: enforcement, Databricks: databricks,
	}

	if err := writeJSONFileAtomic(options.resultPath, output); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	if options.githubOutput != "" {
		if err := writeGitHubOutput(options.githubOutput, options.resultPath, passport, result, enforcement, databricks); err != nil {
			return fmt.Errorf("write GitHub output: %w", err)
		}
	}
	if options.githubSummary != "" {
		if err := appendGitHubSummary(options.githubSummary, passport, result, enforcement, databricks); err != nil {
			return fmt.Errorf("write GitHub summary: %w", err)
		}
	}

	_, _ = fmt.Fprintf(stdout, "ProofGate: %s (automated %s, risk score %d, %d hard stops)\n", enforcement, result.Decision, result.Score, len(result.HardStops))
	if databricks.Status == "DEGRADED" {
		_, _ = fmt.Fprintln(stdout, "Databricks: DEGRADED (continuing with explicitly marked partial evidence)")
	}
	var finalErrors []error
	if options.databricksRequired && len(databricksErrors) > 0 {
		finalErrors = append(finalErrors, fmt.Errorf("required Databricks operation failed: %w", errors.Join(databricksErrors...)))
	}
	if shouldFailEnforcement(options.failOn, result.Decision, databricks.Review) {
		finalErrors = append(finalErrors, policyFailure{status: enforcement})
	}
	return errors.Join(finalErrors...)
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
	flags.StringVar(&options.databricksMode, "databricks-mode", "off", "off, history, export, or roundtrip")
	flags.BoolVar(&options.databricksRequired, "databricks-required", false, "fail CI if a requested Databricks operation is unavailable")
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
	switch options.databricksMode {
	case "off", "history", "export", "roundtrip":
	default:
		return options, fmt.Errorf("invalid --databricks-mode %q", options.databricksMode)
	}
	return options, nil
}

func modeNeedsHistory(mode string) bool {
	return mode == "history" || mode == "roundtrip"
}

func modeNeedsExport(mode string) bool {
	return mode == "export" || mode == "roundtrip"
}

func databricksSuccessStatus(mode string) string {
	switch mode {
	case "history":
		return "HISTORY_LOADED"
	case "export":
		return "EXPORTED"
	case "roundtrip":
		return "ROUNDTRIP_COMPLETE"
	default:
		return "OFF"
	}
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

func enforcementStatus(decision engine.Decision, review *warehouse.ReviewDecision) string {
	if review != nil && review.Available {
		if review.Action == "APPROVE" {
			return "HUMAN_APPROVED"
		}
		if review.Action == "REJECT" {
			return "HUMAN_REJECTED"
		}
	}
	if decision == engine.DecisionApprovalRequired {
		return "AWAITING_APPROVAL"
	}
	return string(decision)
}

func shouldFailEnforcement(threshold string, decision engine.Decision, review *warehouse.ReviewDecision) bool {
	if review != nil && review.Available {
		return review.Action != "APPROVE"
	}
	return shouldFail(threshold, decision)
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

func writeGitHubOutput(path, resultPath string, passport contracts.ChangePassport, result engine.Result, enforcement string, databricks ciDatabricksState) error {
	values := map[string]string{
		"checkpoint_id":      passport.Change.CheckpointID,
		"decision":           string(result.Decision),
		"hard_stops":         strconv.Itoa(len(result.HardStops)),
		"result_path":        resultPath,
		"risk_score":         strconv.Itoa(result.Score),
		"databricks_status":  databricks.Status,
		"enforcement_status": enforcement,
	}
	keys := []string{"decision", "enforcement_status", "risk_score", "hard_stops", "checkpoint_id", "result_path", "databricks_status"}
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

func appendGitHubSummary(path string, passport contracts.ChangePassport, result engine.Result, enforcement string, databricks ciDatabricksState) error {
	var summary strings.Builder
	_, _ = fmt.Fprintf(&summary, "## ProofGate: %s\n\n", markdownText(enforcement))
	_, _ = fmt.Fprintf(&summary, "| Evidence | Value |\n| --- | --- |\n")
	_, _ = fmt.Fprintf(&summary, "| Automated verdict | %s |\n", result.Decision)
	_, _ = fmt.Fprintf(&summary, "| Risk score | %d |\n", result.Score)
	_, _ = fmt.Fprintf(&summary, "| Hard stops | %d |\n", len(result.HardStops))
	_, _ = fmt.Fprintf(&summary, "| Entire checkpoint | `%s` |\n", markdownCell(passport.Change.CheckpointID, "missing"))
	_, _ = fmt.Fprintf(&summary, "| Source adapter | %s |\n", markdownCell(passport.Authoring.SourceAdapter, "unknown"))
	_, _ = fmt.Fprintf(&summary, "| Provenance complete | %t |\n", passport.Authoring.ProvenanceComplete)
	_, _ = fmt.Fprintf(&summary, "| Change size | %d files / %d changed lines |\n", len(passport.Impact.ChangedFiles), passport.Impact.ChangedLineCount)
	_, _ = fmt.Fprintf(&summary, "| Tests | %d total / %d failed |\n", passport.Tests.Total, passport.Tests.Failed)
	_, _ = fmt.Fprintf(&summary, "| Databricks | %s |\n", markdownText(databricks.Status))
	if passport.History.Available {
		_, _ = fmt.Fprintf(&summary, "| Historical baseline | %d changes over %d days |\n", passport.History.BaselineChangeCount, passport.History.BaselineWindowDays)
	}
	if databricks.Review != nil {
		_, _ = fmt.Fprintf(&summary, "| Human review | %s by %s |\n", markdownText(databricks.Review.Action), markdownText(databricks.Review.ActorID))
	}
	summary.WriteString("\n")
	if len(databricks.Warnings) > 0 {
		summary.WriteString("### Evidence availability\n\n")
		for _, warning := range databricks.Warnings {
			_, _ = fmt.Fprintf(&summary, "- **%s** — the local deterministic gate still ran.\n", markdownText(warning))
		}
		summary.WriteString("\n")
	}

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
