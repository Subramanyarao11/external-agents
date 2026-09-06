package warehouse

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/entireio/external-agents/proofgate/contracts"
	"github.com/entireio/external-agents/proofgate/engine"
)

func TestBuildEventDropsRawPathsAndEntities(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	passport := safePassport(now)
	passport.Impact.ChangedFiles = []string{"private/customer/secret.ts"}
	passport.Impact.ImpactedEntities = []string{"PrivateCustomerTokenManager"}
	result, err := engine.Evaluate(passport, engine.DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := BuildEvent(passport, result)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private/customer/secret.ts", "PrivateCustomerTokenManager"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("warehouse event leaked %q: %s", forbidden, data)
		}
	}
	if event.ChangedFileCount != 1 || event.ImpactedEntityCount != 1 || event.PayloadHash == "" {
		t.Fatalf("unexpected safe event: %+v", event)
	}
}

func TestBuildEventRefusesUnsafeExport(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	passport := safePassport(now)
	passport.Safety.SecretDetected = true
	result, err := engine.Evaluate(passport, engine.DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildEvent(passport, result); err == nil {
		t.Fatal("expected secret export to be refused")
	}
}

func safePassport(now time.Time) contracts.ChangePassport {
	return contracts.ChangePassport{
		SchemaVersion: contracts.SchemaVersion,
		EventID:       "evt-warehouse-safe",
		OccurredAt:    now,
		Repository:    contracts.RepositoryIdentity{ID: "demo/mobile", OptedIn: true, IsPublic: true},
		Change:        contracts.ChangeIdentity{CommitSHA: "abc123", CheckpointID: "cp-safe", AIAuthored: true},
		Authoring:     contracts.AuthoringEvidence{SourceAdapter: "github-actions@1", AgentFamily: "claude", ModelFamily: "sonnet", SessionCount: 1, ProvenanceComplete: true},
		Impact:        contracts.ImpactEvidence{ChangedFiles: []string{"copy.json"}, ImpactedEntities: []string{"copy"}, DependencyDepth: 1},
		Tests:         contracts.TestEvidence{Total: 8, RequiredSuites: []string{"unit"}, PassedSuites: []string{"unit"}},
		Safety:        contracts.SafetyEvidence{RedactionVersion: "redact-v1"},
		EvidenceIDs:   []string{"evt-warehouse-safe"},
	}
}
