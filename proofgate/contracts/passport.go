package contracts

import "time"

const SchemaVersion = "1.0"

type ChangePassport struct {
	SchemaVersion string             `json:"schema_version"`
	EventID       string             `json:"event_id"`
	OccurredAt    time.Time          `json:"occurred_at"`
	Repository    RepositoryIdentity `json:"repository"`
	Change        ChangeIdentity     `json:"change"`
	Authoring     AuthoringEvidence  `json:"authoring"`
	Impact        ImpactEvidence     `json:"impact"`
	Tests         TestEvidence       `json:"tests"`
	History       HistoricalEvidence `json:"history"`
	Safety        SafetyEvidence     `json:"safety"`
	EvidenceIDs   []string           `json:"evidence_ids"`
}

type RepositoryIdentity struct {
	ID       string `json:"id"`
	OptedIn  bool   `json:"opted_in"`
	IsPublic bool   `json:"is_public"`
}

type ChangeIdentity struct {
	CommitSHA    string `json:"commit_sha"`
	CheckpointID string `json:"checkpoint_id"`
	Intent       string `json:"intent_summary"`
	AIAuthored   bool   `json:"ai_authored"`
}

type AuthoringEvidence struct {
	SourceAdapter      string   `json:"source_adapter"`
	AgentFamily        string   `json:"agent_family"`
	ModelFamily        string   `json:"model_family"`
	SessionCount       int      `json:"session_count"`
	HandoffCount       int      `json:"handoff_count"`
	ToolCategories     []string `json:"tool_categories"`
	ProvenanceComplete bool     `json:"provenance_complete"`
}

type ImpactEvidence struct {
	ChangedFiles        []string `json:"changed_files"`
	ChangedLineCount    int      `json:"changed_line_count"`
	ImpactedEntities    []string `json:"impacted_entities"`
	DependencyDepth     int      `json:"dependency_depth"`
	SensitiveComponents []string `json:"sensitive_components"`
	DeniedComponents    []string `json:"denied_components"`
}

type TestEvidence struct {
	Total               int      `json:"total"`
	Failed              int      `json:"failed"`
	RequiredTestMissing bool     `json:"required_test_missing"`
	RequiredSuites      []string `json:"required_suites"`
	PassedSuites        []string `json:"passed_suites"`
}

type HistoricalEvidence struct {
	Available            bool      `json:"available"`
	SnapshotAt           time.Time `json:"snapshot_at"`
	BaselineWindowDays   int       `json:"baseline_window_days"`
	BaselineChangeCount  int       `json:"baseline_change_count"`
	NormalFileCountP95   float64   `json:"normal_file_count_p95"`
	NormalImpactCountP95 float64   `json:"normal_impact_count_p95"`
	SimilarChangeCount   int       `json:"similar_change_count"`
	SimilarFailureRate   float64   `json:"similar_failure_rate"`
	ComponentFailureRate float64   `json:"component_failure_rate"`
	RepeatedFailureCount int       `json:"repeated_failure_count"`
	SimilarEvidenceIDs   []string  `json:"similar_evidence_ids"`
}

type SafetyEvidence struct {
	SecretDetected         bool   `json:"secret_detected"`
	RedactionVersion       string `json:"redaction_version"`
	DroppedFieldCount      int    `json:"dropped_field_count"`
	RawContentExported     bool   `json:"raw_content_exported"`
	ExplicitContentConsent bool   `json:"explicit_content_consent"`
}
