package engine

import (
	"testing"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
)

func TestEvaluateAcceptanceScenarios(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	policy := DefaultPolicy()
	tests := []struct {
		name     string
		mutate   func(*contracts.ChangePassport)
		decision Decision
		hardStop string
	}{
		{name: "valid small change", decision: DecisionPass},
		{name: "missing checkpoint", decision: DecisionApprovalRequired, hardStop: "MISSING_ENTIRE_CHECKPOINT", mutate: func(p *contracts.ChangePassport) { p.Change.CheckpointID = "" }},
		{name: "required test fails", decision: DecisionApprovalRequired, hardStop: "REQUIRED_TEST_FAILED", mutate: func(p *contracts.ChangePassport) { p.Tests.Failed = 1 }},
		{name: "secret detected", decision: DecisionApprovalRequired, hardStop: "SECRET_DETECTED", mutate: func(p *contracts.ChangePassport) { p.Safety.SecretDetected = true }},
		{name: "historical blast radius warning", decision: DecisionWarn, mutate: func(p *contracts.ChangePassport) {
			p.History.Available = true
			p.History.SnapshotAt = now
			p.History.BaselineChangeCount = 20
			p.History.NormalFileCountP95 = 2
			p.History.NormalImpactCountP95 = 2
			p.Impact.ChangedFiles = []string{"a.go", "b.go", "c.go"}
			p.Impact.ImpactedEntities = []string{"A", "B", "C"}
		}},
		{name: "similar failures require approval", decision: DecisionApprovalRequired, mutate: func(p *contracts.ChangePassport) {
			p.History.Available = true
			p.History.SnapshotAt = now
			p.History.BaselineChangeCount = 20
			p.History.SimilarChangeCount = 5
			p.History.SimilarFailureRate = .5
			p.History.ComponentFailureRate = .4
			p.History.RepeatedFailureCount = 2
			p.Impact.SensitiveComponents = []string{"authentication"}
			p.Authoring.HandoffCount = 3
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			passport := safePassport(now)
			if test.mutate != nil {
				test.mutate(&passport)
			}
			result, err := Evaluate(passport, policy, now)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.decision {
				t.Fatalf("decision = %s, want %s (score=%d reasons=%+v)", result.Decision, test.decision, result.Score, result.Reasons)
			}
			if test.hardStop != "" && !hasHardStop(result, test.hardStop) {
				t.Fatalf("missing hard stop %s: %+v", test.hardStop, result.HardStops)
			}
		})
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	passport := safePassport(now)
	first, err := Evaluate(passport, DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluate(passport, DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Score != second.Score || first.Decision != second.Decision || first.PassportFingerprint != second.PassportFingerprint {
		t.Fatalf("non-deterministic result: %+v vs %+v", first, second)
	}
}

func safePassport(now time.Time) contracts.ChangePassport {
	return contracts.ChangePassport{
		SchemaVersion: contracts.SchemaVersion,
		EventID:       "evt-safe-1",
		OccurredAt:    now,
		Repository:    contracts.RepositoryIdentity{ID: "demo/mobile", OptedIn: true, IsPublic: true},
		Change:        contracts.ChangeIdentity{CommitSHA: "abc123", CheckpointID: "cp-safe-1", Intent: "Update copy", AIAuthored: true},
		Authoring:     contracts.AuthoringEvidence{SourceAdapter: "github-actions@1", AgentFamily: "claude", ModelFamily: "sonnet", SessionCount: 1, ProvenanceComplete: true},
		Impact:        contracts.ImpactEvidence{ChangedFiles: []string{"copy.json"}, ChangedLineCount: 2, ImpactedEntities: []string{"copy"}, DependencyDepth: 1},
		Tests:         contracts.TestEvidence{Total: 8, Failed: 0, RequiredSuites: []string{"unit"}, PassedSuites: []string{"unit"}},
		Safety:        contracts.SafetyEvidence{RedactionVersion: "redact-v1"},
		EvidenceIDs:   []string{"evt-safe-1"},
	}
}

func hasHardStop(result Result, code string) bool {
	for _, stop := range result.HardStops {
		if stop.Code == code {
			return true
		}
	}
	return false
}
