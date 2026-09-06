package engine

type Policy struct {
	Version                  string  `json:"version"`
	PassMaximum              int     `json:"pass_maximum"`
	WarnMaximum              int     `json:"warn_maximum"`
	IncompleteProvenanceRisk int     `json:"incomplete_provenance_risk"`
	ExcessHandoffRisk        int     `json:"excess_handoff_risk"`
	SensitiveComponentRisk   int     `json:"sensitive_component_risk"`
	MissingTestsRisk         int     `json:"missing_tests_risk"`
	FailedTestsRisk          int     `json:"failed_tests_risk"`
	AbnormalFileCountRisk    int     `json:"abnormal_file_count_risk"`
	AbnormalImpactRisk       int     `json:"abnormal_impact_risk"`
	DeepDependencyRisk       int     `json:"deep_dependency_risk"`
	SimilarFailureRisk       int     `json:"similar_failure_risk"`
	ComponentFailureRisk     int     `json:"component_failure_risk"`
	RepeatedFailureRisk      int     `json:"repeated_failure_risk"`
	SimilarFailureThreshold  float64 `json:"similar_failure_threshold"`
	ComponentFailThreshold   float64 `json:"component_failure_threshold"`
	MaximumHistoryAgeHours   int     `json:"maximum_history_age_hours"`
	MaximumSafeHandoffs      int     `json:"maximum_safe_handoffs"`
	MaximumSafeDepth         int     `json:"maximum_safe_depth"`
}

func DefaultPolicy() Policy {
	return Policy{
		Version:                  "proofgate-default-v1",
		PassMaximum:              29,
		WarnMaximum:              59,
		IncompleteProvenanceRisk: 35,
		ExcessHandoffRisk:        10,
		SensitiveComponentRisk:   20,
		MissingTestsRisk:         25,
		FailedTestsRisk:          50,
		AbnormalFileCountRisk:    15,
		AbnormalImpactRisk:       20,
		DeepDependencyRisk:       10,
		SimilarFailureRisk:       15,
		ComponentFailureRisk:     10,
		RepeatedFailureRisk:      10,
		SimilarFailureThreshold:  0.30,
		ComponentFailThreshold:   0.25,
		MaximumHistoryAgeHours:   24,
		MaximumSafeHandoffs:      2,
		MaximumSafeDepth:         3,
	}
}
