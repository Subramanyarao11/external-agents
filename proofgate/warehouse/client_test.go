package warehouse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/entireio/external-agents/proofgate/engine"
)

func TestClientUsesParameterizedIdempotentMerge(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	passport := safePassport(now)
	result, err := engine.Evaluate(passport, engine.DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := BuildEvent(passport, result)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/2.0/sql/statements" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer authentication")
		}
		var body statementRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Statement, "MERGE INTO bronze_checkpoint_events") || !strings.Contains(body.Statement, "ON target.event_id = source.event_id") {
			t.Errorf("statement is not an idempotent merge: %s", body.Statement)
		}
		if strings.Contains(body.Statement, event.EventID) {
			t.Error("event id was interpolated into SQL instead of parameterized")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"statement_id":"statement-1","status":{"state":"SUCCEEDED"}}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Host: server.URL, Token: "test-token", WarehouseID: "warehouse-1",
		Catalog: "main", Schema: "proofgate", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ingested, err := client.Ingest(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if ingested.State != "SUCCEEDED" || ingested.EventID != event.EventID || ingested.PayloadHash != event.PayloadHash {
		t.Fatalf("unexpected result: %+v", ingested)
	}
}

func TestClientRequiresHTTPS(t *testing.T) {
	if _, err := NewClient(Config{Host: "http://example.com", Token: "x", WarehouseID: "w", Catalog: "c", Schema: "s"}); err == nil {
		t.Fatal("expected an insecure host to be rejected")
	}
}

func TestLookupHistoryUsesOnlyParameterizedAllowlistedFeatures(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	repositoryID := "repo-with-'quoted-value"
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/2.0/sql/statements" {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body statementRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body.Statement, repositoryID) {
			t.Fatal("repository id was interpolated into the SQL statement")
		}
		for _, expected := range []string{"gold_change_risk_features", ":repo_id", "arrays_overlap", "similar_failure_rate"} {
			if !strings.Contains(body.Statement, expected) {
				t.Fatalf("history query missing %q: %s", expected, body.Statement)
			}
		}
		if body.RowLimit != 1 || body.ByteLimit != 16<<10 {
			t.Fatalf("history response was not tightly bounded: %+v", body)
		}
		parameters := map[string]string{}
		for _, parameter := range body.Parameters {
			parameters[parameter.Name] = parameter.Value
		}
		if parameters["repo_id"] != repositoryID || parameters["sensitive_components_json"] != `["auth"]` {
			t.Fatalf("unexpected history parameters: %v", parameters)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
  "statement_id":"history-1",
  "status":{"state":"SUCCEEDED"},
  "result":{"data_array":[["12","4","3","5","0.4","0.25","2","[\"event-a\",\"event-b\"]"]]}
}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Host: server.URL, Token: "test-token", WarehouseID: "warehouse-1",
		Catalog: "main", Schema: "proofgate", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := client.LookupHistory(context.Background(), HistoryQuery{
		RepositoryID: repositoryID, ExcludeEventID: "current-event",
		Before: now, SnapshotAt: now.Add(time.Minute), BaselineWindowDays: 30,
		ChangedFileCount: 3, ImpactedEntityCount: 2, DependencyDepth: 1,
		SensitiveComponents: []string{"auth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !history.Available || history.BaselineChangeCount != 12 || history.SimilarChangeCount != 5 {
		t.Fatalf("unexpected history counts: %+v", history)
	}
	if history.SimilarFailureRate != 0.4 || history.ComponentFailureRate != 0.25 || history.RepeatedFailureCount != 2 {
		t.Fatalf("unexpected history risk: %+v", history)
	}
	if len(history.SimilarEvidenceIDs) != 2 || history.SimilarEvidenceIDs[1] != "event-b" {
		t.Fatalf("unexpected evidence ids: %v", history.SimilarEvidenceIDs)
	}
}

func TestLookupHistoryRejectsMalformedResult(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
  "statement_id":"history-bad",
  "status":{"state":"SUCCEEDED"},
  "result":{"data_array":[["not-an-int","0","0","0","0","0","0","[]"]]}
}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		Host: server.URL, Token: "test-token", WarehouseID: "warehouse-1",
		Catalog: "main", Schema: "proofgate", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	if _, err := client.LookupHistory(context.Background(), HistoryQuery{
		RepositoryID: "demo", Before: now, SnapshotAt: now,
	}); err == nil {
		t.Fatal("expected malformed Databricks history to be rejected")
	}
}

func TestLookupReviewReturnsLatestParameterizedDecision(t *testing.T) {
	eventID := "event-'not-interpolated"
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body statementRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body.Statement, eventID) || !strings.Contains(body.Statement, ":gate_event_id") {
			t.Fatalf("review lookup was not parameterized: %s", body.Statement)
		}
		if len(body.Parameters) != 1 || body.Parameters[0].Value != eventID {
			t.Fatalf("unexpected parameters: %+v", body.Parameters)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
  "statement_id":"review-1",
  "status":{"state":"SUCCEEDED"},
  "result":{"data_array":[["decision-1","APPROVE","reviewer@example.test","2026-09-04 12:30:00+00:00"]]}
}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		Host: server.URL, Token: "test-token", WarehouseID: "warehouse-1",
		Catalog: "main", Schema: "proofgate", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := client.LookupReview(context.Background(), eventID)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Available || review.Action != "APPROVE" || review.DecisionEventID != "decision-1" {
		t.Fatalf("unexpected review: %+v", review)
	}
	if review.OccurredAt.Format(time.RFC3339) != "2026-09-04T12:30:00Z" {
		t.Fatalf("unexpected review timestamp: %s", review.OccurredAt)
	}
}

func TestLookupReviewHandlesPendingGate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
  "statement_id":"review-empty",
  "status":{"state":"SUCCEEDED"},
  "result":{"data_array":[]}
}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		Host: server.URL, Token: "test-token", WarehouseID: "warehouse-1",
		Catalog: "main", Schema: "proofgate", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := client.LookupReview(context.Background(), "event-pending")
	if err != nil {
		t.Fatal(err)
	}
	if review.Available {
		t.Fatalf("pending gate unexpectedly has a review: %+v", review)
	}
}
