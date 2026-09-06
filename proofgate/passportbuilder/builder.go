package passportbuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
)

type Runner interface {
	Run(ctx context.Context, directory, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return nil, fmt.Errorf("%s failed: %s", name, strings.TrimSpace(string(exitError.Stderr)))
	}
	return nil, fmt.Errorf("run %s: %w", name, err)
}

type Builder struct {
	Runner Runner
}

type Request struct {
	RepositoryPath     string
	RepositoryID       string
	Commit             string
	CheckpointID       string
	Intent             string
	RepositoryOptedIn  bool
	RepositoryIsPublic bool
	AIAuthored         bool
	Tests              TestReport
	SensitivePrefixes  []string
	DeniedPrefixes     []string
}

type TestReport struct {
	Suites []TestSuite `json:"suites"`
}

type TestSuite struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Total      int    `json:"total"`
	Failed     int    `json:"failed"`
	Required   bool   `json:"required"`
	EvidenceID string `json:"evidence_id"`
}

type entireExplanation struct {
	CheckpointID string          `json:"checkpoint_id"`
	FilesTouched []string        `json:"files_touched"`
	SessionCount int             `json:"session_count"`
	Sessions     []entireSession `json:"sessions"`
}

type entireSession struct {
	SessionID    string   `json:"session_id"`
	Agent        string   `json:"agent"`
	Model        string   `json:"model"`
	FilesTouched []string `json:"files_touched"`
}

var (
	secretAssignment = regexp.MustCompile(`(?im)^\+.*(api[_-]?key|secret|token|password|private[_-]?key)\s*[:=]\s*["']?[^\s"']{12,}`)
	privateKeyHeader = regexp.MustCompile(`(?m)^\+.*-----BEGIN [A-Z ]*PRIVATE KEY-----`)
)

var defaultSensitivePrefixes = []string{
	"auth", "security", "payments", "payment", "billing", "infra", "migrations", ".github/workflows",
}

func (builder Builder) Build(ctx context.Context, request Request) (contracts.ChangePassport, error) {
	if builder.Runner == nil {
		builder.Runner = ExecRunner{}
	}
	directory, err := filepath.Abs(request.RepositoryPath)
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("resolve repository path: %w", err)
	}
	commit := strings.TrimSpace(request.Commit)
	if commit == "" {
		commit = "HEAD"
	}
	commitOutput, err := builder.Runner.Run(ctx, directory, "git", "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("resolve commit: %w", err)
	}
	commitSHA := strings.TrimSpace(string(commitOutput))

	messageOutput, err := builder.Runner.Run(ctx, directory, "git", "show", "-s", "--format=%B", commitSHA)
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("read commit message: %w", err)
	}
	message := strings.TrimSpace(string(messageOutput))
	checkpointID := strings.TrimSpace(request.CheckpointID)
	if checkpointID == "" || checkpointID == "auto" {
		checkpointID = trailerValue(message, "Entire-Checkpoint")
	}

	var explanation entireExplanation
	if checkpointID != "" {
		explanationOutput, runErr := builder.Runner.Run(
			ctx, directory, "entire", "checkpoint", "explain", checkpointID, "--json",
		)
		if runErr != nil {
			return contracts.ChangePassport{}, fmt.Errorf("explain Entire checkpoint: %w", runErr)
		}
		if decodeErr := json.Unmarshal(explanationOutput, &explanation); decodeErr != nil {
			return contracts.ChangePassport{}, fmt.Errorf("decode Entire checkpoint: %w", decodeErr)
		}
		if explanation.CheckpointID != checkpointID {
			return contracts.ChangePassport{}, fmt.Errorf("Entire checkpoint mismatch")
		}
	}

	filesOutput, err := builder.Runner.Run(
		ctx, directory, "git", "diff-tree", "--root", "--first-parent", "--no-commit-id", "--name-only", "-r", commitSHA,
	)
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("read changed files: %w", err)
	}
	changedFiles := nonEmptyLines(filesOutput)
	slices.Sort(changedFiles)
	changedFiles = slices.Compact(changedFiles)

	numstatOutput, err := builder.Runner.Run(
		ctx, directory, "git", "diff-tree", "--root", "--first-parent", "--no-commit-id", "--numstat", "-r", commitSHA,
	)
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("read change size: %w", err)
	}
	changedLines := parseChangedLines(numstatOutput)

	dateOutput, err := builder.Runner.Run(ctx, directory, "git", "show", "-s", "--format=%cI", commitSHA)
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("read commit time: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339, strings.TrimSpace(string(dateOutput)))
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("parse commit time: %w", err)
	}

	diffOutput, err := builder.Runner.Run(
		ctx, directory, "git", "show", "--format=", "--first-parent", "--unified=0", "--no-ext-diff", commitSHA,
	)
	if err != nil {
		return contracts.ChangePassport{}, fmt.Errorf("inspect diff safety: %w", err)
	}

	repositoryID := strings.TrimSpace(request.RepositoryID)
	if repositoryID == "" {
		repositoryID = filepath.Base(directory)
	}
	intent := strings.TrimSpace(request.Intent)
	if intent == "" {
		intent = firstMessageLine(message)
	}

	sensitivePrefixes := request.SensitivePrefixes
	if sensitivePrefixes == nil {
		sensitivePrefixes = defaultSensitivePrefixes
	}
	tests, testEvidenceIDs, err := request.Tests.evidence()
	if err != nil {
		return contracts.ChangePassport{}, err
	}
	provenanceComplete := checkpointID != "" && explanation.SessionCount > 0 && containsAll(explanation.FilesTouched, changedFiles)
	evidenceIDs := append([]string{}, testEvidenceIDs...)
	if checkpointID != "" {
		evidenceIDs = append(evidenceIDs, checkpointID)
	}

	passport := contracts.ChangePassport{
		SchemaVersion: contracts.SchemaVersion,
		EventID:       eventID(repositoryID, commitSHA, checkpointID),
		OccurredAt:    occurredAt.UTC(),
		Repository: contracts.RepositoryIdentity{
			ID: repositoryID, OptedIn: request.RepositoryOptedIn, IsPublic: request.RepositoryIsPublic,
		},
		Change: contracts.ChangeIdentity{
			CommitSHA: commitSHA, CheckpointID: checkpointID, Intent: intent, AIAuthored: request.AIAuthored || checkpointID != "",
		},
		Authoring: contracts.AuthoringEvidence{
			SourceAdapter:      sourceAdapter(explanation.Sessions),
			AgentFamily:        commonValue(explanation.Sessions, func(session entireSession) string { return session.Agent }),
			ModelFamily:        commonValue(explanation.Sessions, func(session entireSession) string { return session.Model }),
			SessionCount:       explanation.SessionCount,
			HandoffCount:       max(0, explanation.SessionCount-1),
			ToolCategories:     []string{},
			ProvenanceComplete: provenanceComplete,
		},
		Impact: contracts.ImpactEvidence{
			ChangedFiles:        changedFiles,
			ChangedLineCount:    changedLines,
			ImpactedEntities:    impactedEntities(changedFiles),
			DependencyDepth:     dependencyDepth(changedFiles),
			SensitiveComponents: matchingPrefixes(changedFiles, sensitivePrefixes),
			DeniedComponents:    matchingPrefixes(changedFiles, request.DeniedPrefixes),
		},
		Tests:   tests,
		History: contracts.HistoricalEvidence{SimilarEvidenceIDs: []string{}},
		Safety: contracts.SafetyEvidence{
			SecretDetected:         secretAssignment.Match(diffOutput) || privateKeyHeader.Match(diffOutput),
			RedactionVersion:       "proofgate-local-v1",
			RawContentExported:     false,
			ExplicitContentConsent: false,
		},
		EvidenceIDs: evidenceIDs,
	}
	return passport, nil
}

func (report TestReport) evidence() (contracts.TestEvidence, []string, error) {
	evidence := contracts.TestEvidence{RequiredSuites: []string{}, PassedSuites: []string{}}
	ids := []string{}
	for _, suite := range report.Suites {
		name := strings.TrimSpace(suite.Name)
		status := strings.ToLower(strings.TrimSpace(suite.Status))
		if name == "" || suite.Total < 0 || suite.Failed < 0 || suite.Failed > suite.Total {
			return evidence, nil, fmt.Errorf("invalid test suite evidence")
		}
		if status != "passed" && status != "failed" && status != "skipped" {
			return evidence, nil, fmt.Errorf("test suite %q has invalid status %q", name, suite.Status)
		}
		evidence.Total += suite.Total
		evidence.Failed += suite.Failed
		if suite.Required {
			evidence.RequiredSuites = append(evidence.RequiredSuites, name)
			if status != "passed" {
				evidence.RequiredTestMissing = status == "skipped"
			} else {
				evidence.PassedSuites = append(evidence.PassedSuites, name)
			}
		}
		if strings.TrimSpace(suite.EvidenceID) != "" {
			ids = append(ids, strings.TrimSpace(suite.EvidenceID))
		}
	}
	return evidence, ids, nil
}

func trailerValue(message, key string) string {
	prefix := strings.ToLower(key) + ":"
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func firstMessageLine(message string) string {
	for _, line := range strings.Split(message, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			return value
		}
	}
	return ""
}

func nonEmptyLines(value []byte) []string {
	lines := []string{}
	for _, line := range strings.Split(string(value), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseChangedLines(value []byte) int {
	total := 0
	for _, line := range nonEmptyLines(value) {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		for _, count := range fields[:2] {
			if parsed, err := strconv.Atoi(count); err == nil {
				total += parsed
			}
		}
	}
	return total
}

func impactedEntities(files []string) []string {
	entities := []string{}
	for _, file := range files {
		parts := strings.Split(filepath.ToSlash(file), "/")
		entity := parts[0]
		if len(parts) == 1 {
			entity = strings.TrimSuffix(parts[0], filepath.Ext(parts[0]))
		}
		entities = append(entities, entity)
	}
	slices.Sort(entities)
	return slices.Compact(entities)
}

func dependencyDepth(files []string) int {
	depth := 0
	for _, file := range files {
		depth = max(depth, strings.Count(filepath.ToSlash(file), "/"))
	}
	return depth
}

func matchingPrefixes(files, prefixes []string) []string {
	matches := []string{}
	for _, prefix := range prefixes {
		prefix = strings.Trim(strings.TrimSpace(filepath.ToSlash(prefix)), "/")
		if prefix == "" {
			continue
		}
		for _, file := range files {
			file = strings.Trim(filepath.ToSlash(file), "/")
			if file == prefix || strings.HasPrefix(file, prefix+"/") || strings.Contains(file, "/"+prefix+"/") {
				matches = append(matches, prefix)
				break
			}
		}
	}
	slices.Sort(matches)
	return slices.Compact(matches)
}

func containsAll(have, required []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, value := range have {
		set[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func commonValue(sessions []entireSession, value func(entireSession) string) string {
	if len(sessions) == 0 {
		return "unknown"
	}
	common := strings.TrimSpace(value(sessions[0]))
	for _, session := range sessions[1:] {
		if !strings.EqualFold(common, strings.TrimSpace(value(session))) {
			return "mixed"
		}
	}
	return common
}

func sourceAdapter(sessions []entireSession) string {
	agent := strings.ToLower(commonValue(sessions, func(session entireSession) string { return session.Agent }))
	if strings.Contains(agent, "github") && strings.Contains(agent, "action") {
		return "github-actions"
	}
	if agent == "unknown" || agent == "mixed" {
		return agent
	}
	return strings.ReplaceAll(agent, " ", "-")
}

func eventID(repositoryID, commitSHA, checkpointID string) string {
	digest := sha256.Sum256([]byte(repositoryID + "\x00" + commitSHA + "\x00" + checkpointID))
	return "change-" + hex.EncodeToString(digest[:10])
}
