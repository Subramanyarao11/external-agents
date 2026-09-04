package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
)

type Decision string

const (
	DecisionPass             Decision = "PASS"
	DecisionWarn             Decision = "WARN"
	DecisionApprovalRequired Decision = "APPROVAL_REQUIRED"
)

type Result struct {
	Decision               Decision     `json:"decision"`
	Score                  int          `json:"score"`
	PolicyVersion          string       `json:"policy_version"`
	PassportFingerprint    string       `json:"passport_fingerprint"`
	Reasons                []RiskReason `json:"risk_reasons"`
	HardStops              []HardStop   `json:"hard_stops"`
	Uncertainties          []string     `json:"uncertainties"`
	RecommendedReviewFocus []string     `json:"recommended_review_focus"`
	HistorySnapshotAt      *time.Time   `json:"history_snapshot_at,omitempty"`
	EvaluatedAt            time.Time    `json:"evaluated_at"`
}

type RiskReason struct {
	Code        string   `json:"code"`
	Points      int      `json:"points"`
	Message     string   `json:"message"`
	Observed    any      `json:"observed,omitempty"`
	Baseline    any      `json:"baseline,omitempty"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type HardStop struct {
	Code        string   `json:"code"`
	Message     string   `json:"message"`
	EvidenceIDs []string `json:"evidence_ids"`
}

func Evaluate(passport contracts.ChangePassport, policy Policy, now time.Time) (Result, error) {
	if err := validate(passport, policy); err != nil {
		return Result{}, err
	}
	result := Result{
		PolicyVersion:          policy.Version,
		Reasons:                []RiskReason{},
		HardStops:              []HardStop{},
		Uncertainties:          []string{},
		RecommendedReviewFocus: []string{},
		EvaluatedAt:            now.UTC(),
	}
	fingerprint, err := fingerprint(passport)
	if err != nil {
		return Result{}, err
	}
	result.PassportFingerprint = fingerprint
	baseEvidence := slices.Clone(passport.EvidenceIDs)
	if passport.Change.CheckpointID != "" {
		baseEvidence = appendUnique(baseEvidence, passport.Change.CheckpointID)
	}

	addReason := func(code string, points int, message string, observed, baseline any, evidence []string) {
		result.Score += points
		result.Reasons = append(result.Reasons, RiskReason{
			Code: code, Points: points, Message: message, Observed: observed,
			Baseline: baseline, EvidenceIDs: slices.Clone(evidence),
		})
	}
	addHardStop := func(code, message string) {
		result.HardStops = append(result.HardStops, HardStop{Code: code, Message: message, EvidenceIDs: slices.Clone(baseEvidence)})
	}

	if passport.Change.AIAuthored && strings.TrimSpace(passport.Change.CheckpointID) == "" {
		addReason("MISSING_ENTIRE_CHECKPOINT", policy.IncompleteProvenanceRisk, "AI-authored change has no Entire checkpoint.", false, true, baseEvidence)
		addHardStop("MISSING_ENTIRE_CHECKPOINT", "A reviewer must verify provenance before this change can proceed.")
	} else if !passport.Authoring.ProvenanceComplete {
		addReason("INCOMPLETE_PROVENANCE", policy.IncompleteProvenanceRisk, "The authoring trail is incomplete.", false, true, baseEvidence)
	}
	if passport.Authoring.HandoffCount > policy.MaximumSafeHandoffs {
		addReason("EXCESS_AGENT_HANDOFFS", policy.ExcessHandoffRisk, "The change crossed more agent handoffs than policy normally allows.", passport.Authoring.HandoffCount, policy.MaximumSafeHandoffs, baseEvidence)
	}
	if len(passport.Impact.SensitiveComponents) > 0 {
		addReason("SENSITIVE_COMPONENT", policy.SensitiveComponentRisk, "The change reaches a sensitive component.", passport.Impact.SensitiveComponents, nil, baseEvidence)
		result.RecommendedReviewFocus = appendUnique(result.RecommendedReviewFocus, passport.Impact.SensitiveComponents...)
	}
	if len(passport.Impact.DeniedComponents) > 0 {
		addHardStop("POLICY_DENIED_COMPONENT", "The change touches a component that this policy never auto-approves.")
		result.RecommendedReviewFocus = appendUnique(result.RecommendedReviewFocus, passport.Impact.DeniedComponents...)
	}
	if passport.Tests.RequiredTestMissing {
		addReason("REQUIRED_TEST_MISSING", policy.MissingTestsRisk, "A required test suite did not run.", passport.Tests.PassedSuites, passport.Tests.RequiredSuites, baseEvidence)
		addHardStop("REQUIRED_TEST_MISSING", "Run every required suite or obtain an explicit review decision.")
	}
	if passport.Tests.Failed > 0 {
		addReason("REQUIRED_TEST_FAILED", policy.FailedTestsRisk, "At least one test failed.", passport.Tests.Failed, 0, baseEvidence)
		addHardStop("REQUIRED_TEST_FAILED", "A failing test cannot be auto-approved.")
	}
	if passport.Tests.Total == 0 && len(passport.Impact.ChangedFiles) > 0 {
		addReason("NO_TEST_EVIDENCE", policy.MissingTestsRisk, "No test execution evidence accompanies this code change.", 0, ">0", baseEvidence)
	}
	if passport.Impact.DependencyDepth > policy.MaximumSafeDepth {
		addReason("DEEP_DEPENDENCY_REACH", policy.DeepDependencyRisk, "The dependency impact travels deeper than the normal review boundary.", passport.Impact.DependencyDepth, policy.MaximumSafeDepth, baseEvidence)
	}
	if passport.Impact.MaxDependentCount > policy.MaximumSafeDependents {
		addReason("HIGH_DEPENDENT_COUNT", policy.HighDependentRisk, "Entire Graph found a changed entity with an unusually broad dependent surface.", passport.Impact.MaxDependentCount, policy.MaximumSafeDependents, baseEvidence)
	}
	if passport.Impact.AnalysisSource == "git-path-heuristic" && len(passport.Impact.ChangedFiles) > 0 {
		result.Uncertainties = append(result.Uncertainties, "Entire Graph evidence is unavailable; blast radius uses the labelled git-path fallback.")
	} else if strings.HasPrefix(passport.Impact.AnalysisSource, "entire-graph-") && !passport.Impact.AnalysisComplete {
		result.Uncertainties = append(result.Uncertainties, "Entire Graph returned partial coverage; conservative file-level evidence represents uncovered files.")
	}

	if passport.Safety.SecretDetected {
		addHardStop("SECRET_DETECTED", "Potential secret material must be removed before evidence can leave the runner.")
	}
	if passport.Safety.RawContentExported && !passport.Safety.ExplicitContentConsent {
		addHardStop("CONTENT_EXPORT_WITHOUT_CONSENT", "Raw content export is not permitted without repository consent.")
	}
	if !passport.Repository.OptedIn {
		addHardStop("REPOSITORY_NOT_OPTED_IN", "This repository has not opted in to ProofGate export.")
	}

	if !passport.History.Available {
		result.Uncertainties = append(result.Uncertainties, "Historical analysis is unavailable; local policy evidence was still evaluated.")
	} else {
		snapshot := passport.History.SnapshotAt.UTC()
		result.HistorySnapshotAt = &snapshot
		if snapshot.IsZero() || now.Sub(snapshot) > time.Duration(policy.MaximumHistoryAgeHours)*time.Hour {
			result.Uncertainties = append(result.Uncertainties, "Historical risk snapshot is stale.")
		}
		if passport.History.BaselineChangeCount < 5 {
			result.Uncertainties = append(result.Uncertainties, fmt.Sprintf("Historical baseline contains only %d changes.", passport.History.BaselineChangeCount))
		}
		if passport.History.NormalFileCountP95 > 0 && float64(len(passport.Impact.ChangedFiles)) > passport.History.NormalFileCountP95 {
			addReason("ABNORMAL_FILE_COUNT", policy.AbnormalFileCountRisk, "This change modifies more files than the historical p95.", len(passport.Impact.ChangedFiles), passport.History.NormalFileCountP95, historyEvidence(passport, baseEvidence))
		}
		if passport.History.NormalImpactCountP95 > 0 && float64(len(passport.Impact.ImpactedEntities)) > passport.History.NormalImpactCountP95 {
			addReason("ABNORMAL_BLAST_RADIUS", policy.AbnormalImpactRisk, "The impacted-entity count exceeds the historical p95.", len(passport.Impact.ImpactedEntities), passport.History.NormalImpactCountP95, historyEvidence(passport, baseEvidence))
		}
		if passport.History.SimilarChangeCount > 0 && passport.History.SimilarFailureRate >= policy.SimilarFailureThreshold {
			addReason("SIMILAR_CHANGES_FAILED", policy.SimilarFailureRisk, "Similar historical changes failed at an elevated rate.", passport.History.SimilarFailureRate, policy.SimilarFailureThreshold, historyEvidence(passport, baseEvidence))
		}
		if passport.History.ComponentFailureRate >= policy.ComponentFailThreshold {
			addReason("COMPONENT_FAILURE_RATE", policy.ComponentFailureRisk, "The affected component has an elevated failure rate.", passport.History.ComponentFailureRate, policy.ComponentFailThreshold, historyEvidence(passport, baseEvidence))
		}
		if passport.History.RepeatedFailureCount > 0 {
			addReason("REPEATED_FAILURE_PATTERN", policy.RepeatedFailureRisk, "Recent evidence contains a repeated failure pattern.", passport.History.RepeatedFailureCount, 0, historyEvidence(passport, baseEvidence))
		}
	}

	if result.Score > 100 {
		result.Score = 100
	}
	switch {
	case len(result.HardStops) > 0 || result.Score > policy.WarnMaximum:
		result.Decision = DecisionApprovalRequired
	case result.Score > policy.PassMaximum:
		result.Decision = DecisionWarn
	default:
		result.Decision = DecisionPass
	}
	if len(result.RecommendedReviewFocus) == 0 && result.Decision != DecisionPass {
		result.RecommendedReviewFocus = []string{"Validate the highest-scoring evidence and affected tests."}
	}
	return result, nil
}

func validate(passport contracts.ChangePassport, policy Policy) error {
	if passport.SchemaVersion != contracts.SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", passport.SchemaVersion)
	}
	if strings.TrimSpace(passport.EventID) == "" {
		return fmt.Errorf("event_id is required")
	}
	if strings.TrimSpace(passport.Repository.ID) == "" {
		return fmt.Errorf("repository.id is required")
	}
	if policy.Version == "" || policy.PassMaximum < 0 || policy.WarnMaximum <= policy.PassMaximum {
		return fmt.Errorf("invalid policy thresholds")
	}
	for name, value := range map[string]float64{
		"similar_failure_rate":   passport.History.SimilarFailureRate,
		"component_failure_rate": passport.History.ComponentFailureRate,
	} {
		if value < 0 || value > 1 {
			return fmt.Errorf("%s must be between 0 and 1", name)
		}
	}
	return nil
}

func fingerprint(passport contracts.ChangePassport) (string, error) {
	data, err := json.Marshal(passport)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func historyEvidence(passport contracts.ChangePassport, base []string) []string {
	return appendUnique(slices.Clone(base), passport.History.SimilarEvidenceIDs...)
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	result := make([]string, 0, len(values)+len(additions))
	for _, value := range append(values, additions...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
