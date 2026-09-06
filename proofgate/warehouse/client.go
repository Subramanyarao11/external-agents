package warehouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Host        string
	Token       string
	WarehouseID string
	Catalog     string
	Schema      string
	HTTPClient  *http.Client
}

type Client struct {
	config Config
}

type IngestResult struct {
	StatementID string `json:"statement_id"`
	State       string `json:"state"`
	EventID     string `json:"event_id"`
	PayloadHash string `json:"payload_hash"`
}

type statementParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type statementRequest struct {
	WarehouseID string               `json:"warehouse_id"`
	Catalog     string               `json:"catalog"`
	Schema      string               `json:"schema"`
	Statement   string               `json:"statement"`
	WaitTimeout string               `json:"wait_timeout"`
	Disposition string               `json:"disposition"`
	Format      string               `json:"format"`
	Parameters  []statementParameter `json:"parameters"`
}

type statementResponse struct {
	StatementID string `json:"statement_id"`
	Status      struct {
		State string `json:"state"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"status"`
}

func NewClient(config Config) (*Client, error) {
	config.Host = strings.TrimRight(strings.TrimSpace(config.Host), "/")
	for name, value := range map[string]string{
		"host": config.Host, "token": config.Token, "warehouse id": config.WarehouseID,
		"catalog": config.Catalog, "schema": config.Schema,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("Databricks %s is required", name)
		}
	}
	parsed, err := url.Parse(config.Host)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("Databricks host must be an https URL")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 55 * time.Second}
	}
	return &Client{config: config}, nil
}

func (client *Client) Ingest(ctx context.Context, event ChangeEvent) (IngestResult, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return IngestResult{}, err
	}
	request := statementRequest{
		WarehouseID: client.config.WarehouseID,
		Catalog:     client.config.Catalog,
		Schema:      client.config.Schema,
		Statement: `MERGE INTO bronze_checkpoint_events AS target
USING (SELECT
  :event_id AS event_id,
  :event_type AS event_type,
  :schema_version AS schema_version,
  CAST(:occurred_at AS TIMESTAMP) AS occurred_at,
  :repo_id AS repo_id,
  :commit_sha AS commit_sha,
  :checkpoint_id AS checkpoint_id,
  :source_adapter AS source_adapter,
  :policy_version AS policy_version,
  :payload_hash AS payload_hash,
  :redaction_version AS redaction_version,
  CAST(:dropped_field_count AS INT) AS dropped_field_count,
  :payload_json AS payload_json
) AS source
ON target.event_id = source.event_id
WHEN NOT MATCHED THEN INSERT (
  event_id, event_type, schema_version, occurred_at, ingested_at,
  repo_id, commit_sha, checkpoint_id, source_adapter, policy_version,
  payload_hash, redaction_version, dropped_field_count, payload_json
) VALUES (
  source.event_id, source.event_type, source.schema_version, source.occurred_at, current_timestamp(),
  source.repo_id, source.commit_sha, source.checkpoint_id, source.source_adapter, source.policy_version,
  source.payload_hash, source.redaction_version, source.dropped_field_count, source.payload_json
)`,
		WaitTimeout: "50s",
		Disposition: "INLINE",
		Format:      "JSON_ARRAY",
		Parameters: []statementParameter{
			{Name: "event_id", Value: event.EventID, Type: "STRING"},
			{Name: "event_type", Value: event.EventType, Type: "STRING"},
			{Name: "schema_version", Value: event.SchemaVersion, Type: "STRING"},
			{Name: "occurred_at", Value: event.OccurredAt.Format(time.RFC3339Nano), Type: "STRING"},
			{Name: "repo_id", Value: event.RepoID, Type: "STRING"},
			{Name: "commit_sha", Value: event.CommitSHA, Type: "STRING"},
			{Name: "checkpoint_id", Value: event.CheckpointID, Type: "STRING"},
			{Name: "source_adapter", Value: event.SourceAdapter, Type: "STRING"},
			{Name: "policy_version", Value: event.PolicyVersion, Type: "STRING"},
			{Name: "payload_hash", Value: event.PayloadHash, Type: "STRING"},
			{Name: "redaction_version", Value: event.RedactionVersion, Type: "STRING"},
			{Name: "dropped_field_count", Value: fmt.Sprintf("%d", event.DroppedFieldCount), Type: "INT"},
			{Name: "payload_json", Value: string(payload), Type: "STRING"},
		},
	}
	response, err := client.execute(ctx, http.MethodPost, "/api/2.0/sql/statements", request)
	if err != nil {
		return IngestResult{}, err
	}
	for response.Status.State == "PENDING" || response.Status.State == "RUNNING" {
		select {
		case <-ctx.Done():
			return IngestResult{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		response, err = client.execute(ctx, http.MethodGet, "/api/2.0/sql/statements/"+url.PathEscape(response.StatementID), nil)
		if err != nil {
			return IngestResult{}, err
		}
	}
	if response.Status.State != "SUCCEEDED" {
		message := "Databricks statement did not succeed"
		if response.Status.Error != nil && response.Status.Error.Message != "" {
			message = response.Status.Error.Message
		}
		return IngestResult{}, fmt.Errorf("%s (state=%s, statement_id=%s)", message, response.Status.State, response.StatementID)
	}
	return IngestResult{StatementID: response.StatementID, State: response.Status.State, EventID: event.EventID, PayloadHash: event.PayloadHash}, nil
}

func (client *Client) execute(ctx context.Context, method, path string, body any) (statementResponse, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return statementResponse{}, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.config.Host+path, reader)
	if err != nil {
		return statementResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.config.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.config.HTTPClient.Do(request)
	if err != nil {
		return statementResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return statementResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statementResponse{}, fmt.Errorf("Databricks API returned %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var result statementResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return statementResponse{}, fmt.Errorf("decode Databricks response: %w", err)
	}
	return result, nil
}
