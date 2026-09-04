package passportbuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type fakeRunner struct {
	outputs map[string]string
}

func (runner fakeRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	output, ok := runner.outputs[key]
	if !ok {
		return nil, fmt.Errorf("unexpected command: %s", key)
	}
	return []byte(output), nil
}

func TestBuildFromEntireCheckpointAndTestEvidence(t *testing.T) {
	commit := "1234567890abcdef1234567890abcdef12345678"
	explanation, err := json.Marshal(entireExplanation{
		CheckpointID: "01CHECKPOINT",
		FilesTouched: []string{"auth/token.go", "mobile/profile.tsx"},
		SessionCount: 2,
		Sessions: []entireSession{
			{SessionID: "run-1", Agent: "GitHub Actions", Model: "claude-sonnet-4-6", FilesTouched: []string{"auth/token.go"}},
			{SessionID: "run-2", Agent: "GitHub Actions", Model: "claude-sonnet-4-6", FilesTouched: []string{"mobile/profile.tsx"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner{outputs: map[string]string{
		"git rev-parse --verify HEAD^{commit}":                                        commit + "\n",
		"git show -s --format=%B " + commit:                                           "refactor token refresh\n\nEntire-Checkpoint: 01CHECKPOINT\n",
		"entire checkpoint explain 01CHECKPOINT --json":                               string(explanation),
		"git diff-tree --root --first-parent --no-commit-id --name-only -r " + commit: "mobile/profile.tsx\nauth/token.go\n",
		"git diff-tree --root --first-parent --no-commit-id --numstat -r " + commit:   "10\t2\tauth/token.go\n4\t1\tmobile/profile.tsx\n",
		"git show -s --format=%cI " + commit:                                          "2026-09-04T12:00:00+05:30\n",
		"git show --format= --first-parent --unified=0 --no-ext-diff " + commit:       "+return refreshedToken\n",
	}}
	builder := Builder{Runner: runner}
	passport, err := builder.Build(context.Background(), Request{
		RepositoryPath:    ".",
		RepositoryID:      "mobile-app",
		RepositoryOptedIn: true,
		AIAuthored:        true,
		Tests: TestReport{Suites: []TestSuite{
			{Name: "unit", Status: "passed", Total: 18, Required: true, EvidenceID: "run-unit-17"},
			{Name: "e2e", Status: "failed", Total: 4, Failed: 1, Required: true, EvidenceID: "run-e2e-17"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !passport.Authoring.ProvenanceComplete {
		t.Fatal("expected complete provenance")
	}
	if passport.Change.CheckpointID != "01CHECKPOINT" || passport.Authoring.SourceAdapter != "github-actions" {
		t.Fatalf("unexpected Entire evidence: %+v", passport.Authoring)
	}
	if passport.Authoring.HandoffCount != 1 || passport.Authoring.SessionCount != 2 {
		t.Fatalf("unexpected sessions: %+v", passport.Authoring)
	}
	if passport.Impact.ChangedLineCount != 17 {
		t.Fatalf("changed lines = %d", passport.Impact.ChangedLineCount)
	}
	if len(passport.Impact.SensitiveComponents) != 1 || passport.Impact.SensitiveComponents[0] != "auth" {
		t.Fatalf("unexpected sensitive components: %v", passport.Impact.SensitiveComponents)
	}
	if passport.Tests.Total != 22 || passport.Tests.Failed != 1 || passport.Tests.RequiredTestMissing {
		t.Fatalf("unexpected test evidence: %+v", passport.Tests)
	}
	if passport.Safety.SecretDetected {
		t.Fatal("ordinary diff was marked as secret")
	}
	if passport.Change.Intent != "refactor token refresh" {
		t.Fatalf("unexpected intent: %q", passport.Change.Intent)
	}
}

func TestBuildWithoutCheckpointPreservesAIAuthoredSignal(t *testing.T) {
	commit := "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	runner := fakeRunner{outputs: map[string]string{
		"git rev-parse --verify HEAD^{commit}":                                        commit + "\n",
		"git show -s --format=%B " + commit:                                           "agent change without trailer\n",
		"git diff-tree --root --first-parent --no-commit-id --name-only -r " + commit: "main.go\n",
		"git diff-tree --root --first-parent --no-commit-id --numstat -r " + commit:   "1\t0\tmain.go\n",
		"git show -s --format=%cI " + commit:                                          "2026-09-04T12:00:00Z\n",
		"git show --format= --first-parent --unified=0 --no-ext-diff " + commit:       "+package main\n",
	}}
	passport, err := (Builder{Runner: runner}).Build(context.Background(), Request{
		RepositoryPath: ".", RepositoryID: "demo", RepositoryOptedIn: true, AIAuthored: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !passport.Change.AIAuthored || passport.Change.CheckpointID != "" || passport.Authoring.ProvenanceComplete {
		t.Fatalf("missing checkpoint truth was lost: %+v", passport)
	}
}

func TestBuildDetectsSecretShapeWithoutRetainingDiff(t *testing.T) {
	commit := "ffffffffffffffffffffffffffffffffffffffff"
	runner := fakeRunner{outputs: map[string]string{
		"git rev-parse --verify HEAD^{commit}":                                        commit + "\n",
		"git show -s --format=%B " + commit:                                           "bad secret\n",
		"git diff-tree --root --first-parent --no-commit-id --name-only -r " + commit: "config.ts\n",
		"git diff-tree --root --first-parent --no-commit-id --numstat -r " + commit:   "1\t0\tconfig.ts\n",
		"git show -s --format=%cI " + commit:                                          "2026-09-04T12:00:00Z\n",
		"git show --format= --first-parent --unified=0 --no-ext-diff " + commit:       "+api_key = 'abcdefghijklmnop'\n",
	}}
	passport, err := (Builder{Runner: runner}).Build(context.Background(), Request{RepositoryPath: "."})
	if err != nil {
		t.Fatal(err)
	}
	if !passport.Safety.SecretDetected {
		t.Fatal("expected secret-shaped added line to be detected")
	}
	encoded, err := json.Marshal(passport)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "abcdefghijklmnop") {
		t.Fatal("secret value leaked into passport")
	}
}

func TestSkippedRequiredSuiteIsMarkedMissing(t *testing.T) {
	evidence, _, err := (TestReport{Suites: []TestSuite{
		{Name: "integration", Status: "skipped", Required: true},
	}}).evidence()
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.RequiredTestMissing {
		t.Fatal("required skipped suite must be missing")
	}
}
