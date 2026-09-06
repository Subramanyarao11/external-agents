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
