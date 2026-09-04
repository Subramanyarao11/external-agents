package warehouse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
	"github.com/entireio/external-agents/proofgate/engine"
)

const (
	EventType        = "change_passport_evaluated"
	RedactionVersion = "proofgate-allowlist-v1"
)

type ChangeEvent struct {
	EventID                string          `json:"event_id"`
	EventType              string          `json:"event_type"`
	SchemaVersion          string          `json:"schema_version"`
	OccurredAt             time.Time       `json:"occurred_at"`
	RepoID                 string          `json:"repo_id"`
	CommitSHA              string          `json:"commit_sha"`
	CheckpointID           string          `json:"checkpoint_id"`
	SourceAdapter          string          `json:"source_adapter"`
	PolicyVersion          string          `json:"policy_version"`
	RedactionVersion       string          `json:"redaction_version"`
	DroppedFieldCount      int             `json:"dropped_field_count"`
	AgentFamily            string          `json:"agent_family"`
	ModelFamily            string          `json:"model_family"`
	SessionCount           int             `json:"session_count"`
	HandoffCount           int             `json:"handoff_count"`
	ToolCategories         []string        `json:"tool_categories"`
	ChangedFileCount       int             `json:"changed_file_count"`
	ChangedLineCount       int             `json:"changed_line_count"`
	ImpactedEntityCount    int             `json:"impacted_entity_count"`
	DependencyDepth        int             `json:"dependency_depth"`
	MaxDependentCount      int             `json:"max_dependent_count"`
	ImpactAnalysisSource   string          `json:"impact_analysis_source"`
	ImpactAnalysisComplete bool            `json:"impact_analysis_complete"`
	SensitiveComponents    []string        `json:"sensitive_components"`
	TestTotal              int             `json:"test_total"`
	TestFailed             int             `json:"test_failed"`
	RequiredTestMissing    bool            `json:"required_test_missing"`
	ProvenanceComplete     bool            `json:"provenance_complete"`
	Decision               engine.Decision `json:"decision"`
	Score                  int             `json:"score"`
	ReasonCodes            []string        `json:"reason_codes"`
	HardStopCodes          []string        `json:"hard_stop_codes"`
	HistoryAvailable       bool            `json:"history_available"`
	HistorySnapshotAt      *time.Time      `json:"history_snapshot_at,omitempty"`
	BaselineChangeCount    int             `json:"baseline_change_count"`
	SimilarChangeCount     int             `json:"similar_change_count"`
	SimilarFailureRate     float64         `json:"similar_failure_rate"`
	ComponentFailureRate   float64         `json:"component_failure_rate"`
	SimilarEvidenceIDs     []string        `json:"similar_evidence_ids"`
	PassportFingerprint    string          `json:"passport_fingerprint"`
	SafeSimilaritySummary  string          `json:"safe_similarity_summary"`
	PayloadHash            string          `json:"payload_hash"`
}

func BuildEvent(passport contracts.ChangePassport, result engine.Result) (ChangeEvent, error) {
	if !passport.Repository.OptedIn {
		return ChangeEvent{}, errors.New("repository has not opted in to Databricks export")
	}
	if passport.Safety.SecretDetected {
		return ChangeEvent{}, errors.New("potential secret detected; export refused")
	}
	if passport.Safety.RawContentExported && !passport.Safety.ExplicitContentConsent {
		return ChangeEvent{}, errors.New("raw content export lacks explicit consent")
	}

	reasons := make([]string, 0, len(result.Reasons))
	for _, reason := range result.Reasons {
		reasons = append(reasons, reason.Code)
	}
	hardStops := make([]string, 0, len(result.HardStops))
	for _, stop := range result.HardStops {
		hardStops = append(hardStops, stop.Code)
	}
	event := ChangeEvent{
		EventID:                passport.EventID,
		EventType:              EventType,
		SchemaVersion:          passport.SchemaVersion,
		OccurredAt:             passport.OccurredAt.UTC(),
		RepoID:                 passport.Repository.ID,
		CommitSHA:              passport.Change.CommitSHA,
		CheckpointID:           passport.Change.CheckpointID,
		SourceAdapter:          passport.Authoring.SourceAdapter,
		PolicyVersion:          result.PolicyVersion,
		RedactionVersion:       RedactionVersion,
		DroppedFieldCount:      passport.Safety.DroppedFieldCount + len(passport.Impact.ChangedFiles) + len(passport.Impact.ImpactedEntities),
		AgentFamily:            passport.Authoring.AgentFamily,
		ModelFamily:            passport.Authoring.ModelFamily,
		SessionCount:           passport.Authoring.SessionCount,
		HandoffCount:           passport.Authoring.HandoffCount,
		ToolCategories:         copyStrings(passport.Authoring.ToolCategories),
		ChangedFileCount:       len(passport.Impact.ChangedFiles),
		ChangedLineCount:       passport.Impact.ChangedLineCount,
		ImpactedEntityCount:    len(passport.Impact.ImpactedEntities),
		DependencyDepth:        passport.Impact.DependencyDepth,
		MaxDependentCount:      passport.Impact.MaxDependentCount,
		ImpactAnalysisSource:   passport.Impact.AnalysisSource,
		ImpactAnalysisComplete: passport.Impact.AnalysisComplete,
		SensitiveComponents:    copyStrings(passport.Impact.SensitiveComponents),
		TestTotal:              passport.Tests.Total,
		TestFailed:             passport.Tests.Failed,
		RequiredTestMissing:    passport.Tests.RequiredTestMissing,
		ProvenanceComplete:     passport.Authoring.ProvenanceComplete,
		Decision:               result.Decision,
		Score:                  result.Score,
		ReasonCodes:            reasons,
		HardStopCodes:          hardStops,
		HistoryAvailable:       passport.History.Available,
		HistorySnapshotAt:      result.HistorySnapshotAt,
		BaselineChangeCount:    passport.History.BaselineChangeCount,
		SimilarChangeCount:     passport.History.SimilarChangeCount,
		SimilarFailureRate:     passport.History.SimilarFailureRate,
		ComponentFailureRate:   passport.History.ComponentFailureRate,
		SimilarEvidenceIDs:     copyStrings(passport.History.SimilarEvidenceIDs),
		PassportFingerprint:    result.PassportFingerprint,
		SafeSimilaritySummary:  similaritySummary(passport, result),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return ChangeEvent{}, err
	}
	digest := sha256.Sum256(payload)
	event.PayloadHash = "sha256:" + hex.EncodeToString(digest[:])
	return event, nil
}

func similaritySummary(passport contracts.ChangePassport, result engine.Result) string {
	parts := []string{
		"agent=" + safeCategory(passport.Authoring.AgentFamily),
		"decision=" + string(result.Decision),
		"blast_radius=" + bucket(len(passport.Impact.ImpactedEntities)),
		"change_size=" + bucket(len(passport.Impact.ChangedFiles)),
	}
	for _, component := range passport.Impact.SensitiveComponents {
		parts = append(parts, "sensitive="+safeCategory(component))
	}
	for _, reason := range result.Reasons {
		parts = append(parts, "reason="+safeCategory(reason.Code))
	}
	return strings.Join(parts, " ")
}

func bucket(value int) string {
	switch {
	case value <= 2:
		return "small"
	case value <= 6:
		return "medium"
	default:
		return "large"
	}
}

func safeCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func copyStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}
